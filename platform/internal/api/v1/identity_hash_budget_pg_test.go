package v1_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Observe HashPassword before Argon2 allocates. Go1.25 rand.Read fatals on a
// Reader error, so this test-only observer panics with a fixed safe marker;
// the real router Recovery catches it. No password/token is captured or logged.
// Other entropy users (request IDs) still use the original secure Reader.
type passwordHashObserver struct {
	base    io.Reader
	calls   atomic.Int64
	entered chan int64
	release <-chan struct{}
}

func (o *passwordHashObserver) Read(p []byte) (int, error) {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.Function == "github.com/yuqing/platform/internal/platform/auth.HashPassword" {
			count := o.calls.Add(1)
			if o.entered != nil {
				o.entered <- count
			}
			if o.release != nil {
				<-o.release
			}
			panic("isolated password hash observer")
		}
		if !more {
			break
		}
	}
	return o.base.Read(p)
}
func hashBudgetRequest(ctx context.Context, e *billingActorPGEnv, body map[string]string, peer string, forged int) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password-reset/confirm", strings.NewReader(string(raw))).WithContext(ctx)
	req.RemoteAddr = peer
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", forged))
	req.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", forged))
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}
func assertHashBudgetStateUnchanged(t *testing.T, e *billingActorPGEnv, uid any) {
	t.Helper()
	var unchanged bool
	if err := e.pool.QueryRow(context.Background(), `SELECT token_version=0 AND row_version=0 AND (SELECT bool_and(used_at IS NULL) FROM verification_tokens WHERE user_id=users.id) FROM users WHERE id=$1`, uid).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if !unchanged {
		t.Fatal("rejected/failed hash mutated identity or consumed credential")
	}
}
func seedHashBudgetCredential(t *testing.T) (*billingActorPGEnv, any, string) {
	t.Helper()
	e := newBillingActorPGEnv(t)
	box := &identitySandbox{}
	configureIdentity(e, box)
	_, _, u := mustRegister(t, e.router, "hash-budget@example.invalid", "Hash budget")
	if w := identityRequest(t, e, "/auth/password-reset/request", `{"email":"hash-budget@example.invalid"}`, 1); w.Code != 202 {
		t.Fatal(w.Code)
	}
	return e, u["user_id"], box.delivered(t)
}

func TestIdentityHashBudgetPGInvalidRotatingCredentialsRejectBeforeKDF(t *testing.T) {
	e, uid, valid := seedHashBudgetCredential(t)
	observer := &passwordHashObserver{base: rand.Reader}
	rand.Reader = observer
	defer func() { rand.Reader = observer.base }()
	statuses := make([]int, 0, 22)
	for i := 0; i < 22; i++ {
		body := map[string]string{"token": fmt.Sprintf("nonexistent-%d", i), "new_password": "Example12345", "ip": fmt.Sprintf("198.51.100.%d", i+1)}
		if i%2 == 1 {
			delete(body, "token")
			body["phone"] = fmt.Sprintf("1390000%04d", i)
			body["code"] = "123456"
		}
		// A known valid bearer and an unknown phone meet the same exhausted source
		// budget; the bearer itself must not be an admission-key selector.
		if i == 20 {
			body["token"] = valid
		}
		w := hashBudgetRequest(context.Background(), e, body, "192.0.2.77:1234", i+1)
		statuses = append(statuses, w.Code)
		if i < 20 {
			want := 400
			if i%2 == 1 {
				want = 401
			}
			if w.Code != want {
				t.Errorf("invalid request%d reached costly path: status=%d want=%d", i+1, w.Code, want)
			}
		} else {
			retry, _ := strconv.Atoi(w.Header().Get("Retry-After"))
			if w.Code != 429 || retry < 1 || retry > 60 {
				t.Errorf("exhausted source request%d: status=%d retry=%d", i+1, w.Code, retry)
			}
		}
	}
	if n := observer.calls.Load(); n != 0 {
		t.Errorf("invalid/over-budget credentials entered HashPassword %d times", n)
	}
	// Positive control proves the observer actually detects the real hasher, even
	// though no costly Argon2 call runs. A different trusted peer can be admitted.
	before := observer.calls.Load()
	w := hashBudgetRequest(context.Background(), e, map[string]string{"token": valid, "new_password": "Example12345"}, "192.0.2.88:1234", 99)
	if w.Code != 500 || observer.calls.Load() != before+1 {
		t.Fatal("positive control did not observe the real HashPassword boundary")
	}
	assertHashBudgetStateUnchanged(t, e, uid)
	t.Logf("safe confirmation statuses=%v observed_hash_entries=%d positive_control=1", statuses, before)
}

func TestIdentityHashBudgetPGConcurrentWorkIsBoundedAndCancellationCannotFreeLiveHash(t *testing.T) {
	e, uid, valid := seedHashBudgetCredential(t)
	release := make(chan struct{})
	observer := &passwordHashObserver{base: rand.Reader, entered: make(chan int64, 8), release: release}
	rand.Reader = observer
	var workers sync.WaitGroup
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); workers.Wait(); rand.Reader = observer.base }()
	call := func(ctx context.Context, peer string) <-chan *httptest.ResponseRecorder {
		out := make(chan *httptest.ResponseRecorder, 1)
		workers.Add(1)
		go func() {
			defer workers.Done()
			out <- hashBudgetRequest(ctx, e, map[string]string{"token": valid, "new_password": "Example12345"}, peer, 1)
		}()
		return out
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := call(ctx, "192.0.2.91:1234")
	second := call(context.Background(), "192.0.2.92:1234")
	for i := 0; i < 2; i++ {
		select {
		case <-observer.entered:
		case <-time.After(10 * time.Second):
			t.Fatal("valid positive control never reached blocking hasher")
		}
	}
	cancel() // An admitted KDF is not interrupted by cancellation; its slot stays.
	third := call(context.Background(), "192.0.2.93:1234")
	thirdDone := false
	select {
	case w := <-third:
		thirdDone = true
		if w.Code != 429 || w.Header().Get("Retry-After") == "" {
			t.Errorf("bounded concurrent rejection missing: %d", w.Code)
		}
	case <-observer.entered:
		t.Errorf("third concurrent request entered hasher; cancellation prematurely freed or no ceiling")
	case <-time.After(10 * time.Second):
		t.Error("excess work waited instead of immediate bounded rejection")
	}
	if observer.calls.Load() != 2 {
		t.Errorf("concurrent hash entries=%d want=2", observer.calls.Load())
	}
	once.Do(func() { close(release) })
	<-first
	<-second
	if !thirdDone {
		<-third
	}
	workers.Wait()
	assertHashBudgetStateUnchanged(t, e, uid)
	// Panic/cancellation cleanup must release capacity only when admitted work
	// actually exits; this fourth request can enter afterwards.
	before := observer.calls.Load()
	fourth := call(context.Background(), "192.0.2.94:1234")
	w := <-fourth
	workers.Wait()
	if w.Code != 500 || observer.calls.Load() != before+1 {
		t.Fatal("completed/canceled work leaked capacity")
	}
	t.Logf("safe bounded-work maximum_expected=2 observed_before_release=%d subsequent_entry=1", before)
}
