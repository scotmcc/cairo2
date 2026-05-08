package server

import (
	"crypto/rand"
	"encoding/hex"
)

// GenerateToken returns a cryptographically random bearer token.
// TokenBytes random bytes are encoded as a lowercase hex string.
func GenerateToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
