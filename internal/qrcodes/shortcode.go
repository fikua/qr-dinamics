package qrcodes

import (
	"crypto/rand"
	"math/big"
)

// codeAlphabet leaves out characters that are easy to confuse when read or
// typed: 0/O/o, 1/l/I/i (RF-06).
const codeAlphabet = "23456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ"

// CodeLength is the length of a short code: 54^7 ≈ 1.3·10^12 combinations.
const CodeLength = 7

// NewCode returns a random short code. Uniqueness is enforced by the database.
func NewCode() (string, error) {
	max := big.NewInt(int64(len(codeAlphabet)))
	b := make([]byte, CodeLength)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = codeAlphabet[n.Int64()]
	}
	return string(b), nil
}

// ValidCode reports whether s has the shape of a short code; the redirect
// service uses it to answer garbage without a lookup.
func ValidCode(s string) bool {
	if len(s) != CodeLength {
		return false
	}
	for i := range len(s) {
		if !isCodeChar(s[i]) {
			return false
		}
	}
	return true
}

func isCodeChar(c byte) bool {
	for i := range len(codeAlphabet) {
		if codeAlphabet[i] == c {
			return true
		}
	}
	return false
}
