// Package id provides ULID generation and parsing.
// ULIDs are 26-character, URL-safe, sortable identifiers based on timestamp + randomness.
package id

import (
	"crypto/rand"
	"math/big"
	"time"
)

const (
	// Length of a ULID string (base32 encoded).
	Length = 26
	// Timestamp portion is 10 chars, randomness is 16 chars.
	timestampLen = 10
	randomLen = 16
)

// Crockford base32 alphabet used by ULID.
const encoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// New generates a new ULID string from the current UTC timestamp and
// 80 bits of cryptographic randomness.
func New() string {
	ts := uint64(time.Now().UTC().UnixMilli())
	dst := make([]byte, Length)

	// Encode timestamp (high 48 bits into 10 characters).
	for i := timestampLen - 1; i >= 0; i-- {
		dst[i] = encoding[ts%32]
		ts /= 32
	}

	// Encode randomness (80 bits into 16 characters).
	for i := timestampLen; i < Length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(32))
		if err != nil {
			// crypto/rand fails only if system entropy is unavailable,
			// which means the host is fundamentally broken. Panic is appropriate.
			panic("id: crypto/rand read failed: " + err.Error())
		}
		dst[i] = encoding[n.Int64()]
	}

	return string(dst)
}
