package id

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	uid := New()
	if uid == "" {
		t.Fatal("New() returned empty string")
	}
	if len(uid) != 26 {
		t.Fatalf("New() returned %d chars, want 26", len(uid))
	}
}

func TestNew_isSortable(t *testing.T) {
	// ULIDs must be sortable by string comparison.
	// First generated before second should be lexicographically smaller.
	first := New()
	time.Sleep(2 * time.Millisecond) // ensure different timestamp
	second := New()

	if first >= second {
		t.Fatalf("ULIDs not sortable: first=%q >= second=%q", first, second)
	}
}

func TestNew_isUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		uid := New()
		if seen[uid] {
			t.Fatalf("duplicate ULID: %q at iteration %d", uid, i)
		}
		seen[uid] = true
	}
}

func TestNew_encodesCrockfordBase32(t *testing.T) {
	uid := New()
	const validChars = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	for i, c := range uid {
		if !containsRune(validChars, c) {
			t.Fatalf("invalid character %q at position %d in %q", c, i, uid)
		}
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
