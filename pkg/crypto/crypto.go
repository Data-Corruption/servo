// Package crypto provides small hashing/token helpers used by the HTTP auth
// stack (session tokens + Argon2id password hashing).
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/argon2"
)

// Hash returns the hex-encoded SHA256 hash of the input string.
// This is a fast, non-reversible hash suitable for indexing cryptographically
// random tokens. For password hashing, use HashPassword instead.
func Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// GenRandomString generates a cryptographically secure URL and filename random token of the given size.
func GenRandomString(size int) (string, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashPassword returns the hashed password and the salt used to hash it.
// Uses Argon2id (RFC 9106's recommended variant): side-channel resistant on
// the first pass, memory-hard against GPU/TMTO attacks on the rest.
func HashPassword(password string) (string, string, error) {
	salt, err := GenRandomString(16)
	if err != nil {
		return "", "", err
	}
	hash := argon2.IDKey([]byte(password), []byte(salt), 3, 32*1024, 4, 32)
	return base64.URLEncoding.EncodeToString(hash), salt, nil
}

// ComparePasswords returns true if the plaintext password matches the given hash and salt when hashed.
func ComparePasswords(password, passHash, passSalt string) bool {
	expected, err := base64.URLEncoding.DecodeString(passHash)
	if err != nil {
		return false
	}
	hash := argon2.IDKey([]byte(password), []byte(passSalt), 3, 32*1024, 4, 32)
	return subtle.ConstantTimeCompare(hash, expected) == 1
}
