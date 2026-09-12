package id

import (
	"testing"
	"time"
)

// encodeULIDTimestamp 是测试内置的、独立于 Time() 的编码实现：把毫秒
// 时间戳写成 10 字符 Crockford base32（ULID 规范的时间戳段）。
// 两个实现互不引用，往返一致才说明解码正确。
func encodeULIDTimestamp(ms uint64) string {
	buf := make([]byte, timestampLen)
	for i := timestampLen - 1; i >= 0; i-- {
		buf[i] = encoding[ms%32]
		ms /= 32
	}
	return string(buf)
}

func TestTime_roundTripsTimestampEncoding(t *testing.T) {
	want := time.Date(2026, 9, 13, 12, 34, 56, 789*int(time.Millisecond), time.UTC)

	ulid := encodeULIDTimestamp(uint64(want.UnixMilli())) + "0000000000000000"
	got, ok := Time(ulid)
	if !ok {
		t.Fatalf("Time(%q) reported not-a-ULID", ulid)
	}
	if !got.Equal(want) {
		t.Errorf("Time() = %s, want %s", got, want)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %s, want UTC", got.Location())
	}
}

// ULID 时间戳是毫秒精度：亚毫秒部分必须被截断而不是四舍五入。
func TestTime_truncatesToMilliseconds(t *testing.T) {
	want := time.Date(2026, 1, 2, 3, 4, 5, 999_999_000, time.UTC)

	ulid := encodeULIDTimestamp(uint64(want.UnixMilli())) + "0000000000000000"
	got, ok := Time(ulid)
	if !ok {
		t.Fatal("Time() reported not-a-ULID")
	}
	if got.Nanosecond() != 999_000_000 {
		t.Errorf("Nanosecond = %d, want 999000000 (truncated to ms)", got.Nanosecond())
	}
}

// id.New() 是唯一的生产者：它产出的 ID 必须能被解出自己的生成时刻。
// 这是「apikey 表无 created_at 列时按 ULID 还原创建时间」的根据。
func TestTime_acceptsGeneratedULIDs(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got, ok := Time(New())
	after := time.Now().UTC().Add(time.Second)

	if !ok {
		t.Fatal("Time(New()) reported not-a-ULID")
	}
	if got.Before(before) || got.After(after) {
		t.Errorf("Time(New()) = %s, want between %s and %s", got, before, after)
	}
}

// Crockford base32 大小写不敏感（编码器只产出大写，解码器两种都接受）。
func TestTime_acceptsLowerCase(t *testing.T) {
	ulid := encodeULIDTimestamp(uint64(time.Now().UnixMilli())) + "0000000000000000"
	upper, okUpper := Time(ulid)
	lower, okLower := Time(stringLower(ulid))

	if !okUpper || !okLower {
		t.Fatalf("ok(upper) = %v, ok(lower) = %v, want both true", okUpper, okLower)
	}
	if !upper.Equal(lower) {
		t.Errorf("upper = %s, lower = %s, want equal", upper, lower)
	}
}

func TestTime_rejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"too short", "01ARZ3NDE"}, // 9 字符 < 10 字符时间戳段
		{"not base32", "!!!!!!!!!!!!!!!!!!!!!!!!!"},
		{"ambiguous letter I", "IIIIIIIIIIIIIIIIIIIIIIIIII"},
		{"ambiguous letter U", "UUUUUUUUUUUUUUUUUUUUUUUUUU"},
		{"punctuation inside timestamp", "01ARZ3N-KTSV4RFFQ69G5FAV"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, ok := Time(tc.in); ok {
				t.Errorf("Time(%q) = %s, ok = true, want ok = false", tc.in, got)
			}
		})
	}
}

func stringLower(s string) string {
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}
