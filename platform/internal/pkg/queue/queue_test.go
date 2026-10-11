package queue

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryQueue_publishSubscribe(t *testing.T) {
	q := NewMemory()
	defer q.Close()

	var received []string
	var mu sync.Mutex

	err := q.Subscribe(context.Background(), "test.topic", func(ctx context.Context, msg Message) error {
		mu.Lock()
		received = append(received, string(msg.Body))
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Give subscriber goroutine time to start.
	time.Sleep(50 * time.Millisecond)

	err = q.Publish(context.Background(), "test.topic", []byte("hello"))
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	err = q.Publish(context.Background(), "test.topic", []byte("world"))
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("received %d messages, want 2: %v", len(received), received)
	}
	if received[0] != "hello" || received[1] != "world" {
		t.Errorf("received = %v, want [hello world]", received)
	}
}

func TestMemoryQueue_multipleTopics(t *testing.T) {
	q := NewMemory()
	defer q.Close()

	var t1Msgs, t2Msgs []string
	var mu sync.Mutex

	q.Subscribe(context.Background(), "topic.one", func(ctx context.Context, msg Message) error {
		mu.Lock()
		t1Msgs = append(t1Msgs, string(msg.Body))
		mu.Unlock()
		return nil
	})
	q.Subscribe(context.Background(), "topic.two", func(ctx context.Context, msg Message) error {
		mu.Lock()
		t2Msgs = append(t2Msgs, string(msg.Body))
		mu.Unlock()
		return nil
	})

	time.Sleep(50 * time.Millisecond)
	q.Publish(context.Background(), "topic.one", []byte("A"))
	q.Publish(context.Background(), "topic.two", []byte("B"))
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(t1Msgs) != 1 || t1Msgs[0] != "A" {
		t.Errorf("topic.one = %v, want [A]", t1Msgs)
	}
	if len(t2Msgs) != 1 || t2Msgs[0] != "B" {
		t.Errorf("topic.two = %v, want [B]", t2Msgs)
	}
}

func TestMemoryQueue_publishBeforeSubscribe_buffers(t *testing.T) {
	q := NewMemory()
	defer q.Close()

	// Publish before any subscriber.
	if err := q.Publish(context.Background(), "late.topic", []byte("buffered")); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	received := make(chan string, 1)
	if err := q.Subscribe(context.Background(), "late.topic", func(ctx context.Context, msg Message) error {
		received <- string(msg.Body)
		return nil
	}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	select {
	case body := <-received:
		if body != "buffered" {
			t.Errorf("received = %q, want buffered", body)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered message")
	}
}

func TestMemoryQueue_closeStopsDelivery(t *testing.T) {
	q := NewMemory()
	defer q.Close()

	var count atomic.Int32
	delivered := make(chan struct{}, 1)
	if err := q.Subscribe(context.Background(), "closing", func(ctx context.Context, msg Message) error {
		count.Add(1)
		delivered <- struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	if err := q.Publish(context.Background(), "closing", []byte("msg1")); err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first message")
	}
	if err := q.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Try to publish after close.
	err := q.Publish(context.Background(), "closing", []byte("msg2"))
	if err == nil {
		t.Error("expected error when publishing to closed queue")
	}

	if got := count.Load(); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
}
