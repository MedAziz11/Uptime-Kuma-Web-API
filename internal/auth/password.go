// Package auth hashes passwords (bcrypt), issues and verifies access tokens
// (JWT, HS256) and resolves the token signing key, with the formats of 2.0
// (app/security.py, app/config.py) so that its users, hashes, tokens and
// key file keep working after an upgrade.
package auth

import (
	"bytes"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// MaxPasswordBytes is bcrypt's limit. Longer passwords are rejected rather
// than silently truncated, as in 2.0.
const MaxPasswordBytes = 72

// DefaultCost is the bcrypt cost 2.0 uses (Python bcrypt's default).
const DefaultCost = 12

// ErrPasswordTooLong is returned by Hash for a password over MaxPasswordBytes.
var ErrPasswordTooLong = errors.New("auth: password longer than 72 bytes")

// Passwords hashes and verifies passwords at a fixed bcrypt cost.
type Passwords struct {
	cost int
	// dummy is verified against when a user has no hash (unknown user,
	// NULL column), so that the time taken does not reveal it.
	dummy []byte
}

// NewPasswords returns a Passwords hashing at cost (DefaultCost in
// production; bcrypt.MinCost keeps tests fast).
func NewPasswords(cost int) (*Passwords, error) {
	dummy, err := bcrypt.GenerateFromPassword([]byte("dummy-password"), cost)
	if err != nil {
		return nil, fmt.Errorf("auth: bcrypt cost %d: %w", cost, err)
	}
	return &Passwords{cost: cost, dummy: dummy}, nil
}

// Hash returns the bcrypt hash of password in 2.0's format ("$2b$12$...").
func (p *Passwords) Hash(password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), p.cost)
	if err != nil {
		return "", fmt.Errorf("auth: hash password: %w", err)
	}
	// x/crypto writes the "2a" variant. "2b" only differs from it for
	// passwords of 255 bytes or more, which are rejected above, so the hash
	// is the same and the prefix is the one 2.0's bcrypt writes.
	if bytes.HasPrefix(h, []byte("$2a$")) {
		h[2] = 'b'
	}
	return string(h), nil
}

// Verify reports whether password matches hash. An empty hash (unknown user
// or no password) never matches but costs the same time as a real check.
// A password over MaxPasswordBytes or a malformed hash never matches.
func (p *Passwords) Verify(password, hash string) bool {
	if len(password) > MaxPasswordBytes {
		return false
	}
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(p.dummy, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
