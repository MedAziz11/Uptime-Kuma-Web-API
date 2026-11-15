// Package auth hashes passwords (bcrypt), issues and verifies access tokens
// (JWT, HS256) and resolves the token signing key, with the formats of 2.0
// (app/security.py, app/config.py) so that its users, hashes, tokens and
// key file keep working after an upgrade.
package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"

	"golang.org/x/crypto/bcrypt"
)

// MaxPasswordBytes is bcrypt's limit. Longer passwords are rejected rather
// than silently truncated, as in 2.0.
const MaxPasswordBytes = 72

// DefaultCost is the bcrypt cost 2.0 uses (Python bcrypt's default).
const DefaultCost = 12

// ErrPasswordTooLong is returned by Hash for a password over MaxPasswordBytes.
var ErrPasswordTooLong = errors.New("auth: password longer than 72 bytes")

// ErrBusy is returned when no bcrypt slot became free before the context
// ended (see Passwords).
var ErrBusy = errors.New("auth: too many password checks in progress")

// Passwords hashes and verifies passwords at a fixed bcrypt cost.
//
// A bcrypt operation costs a CPU for about 250 to 450 ms at cost 12, and
// logins are unauthenticated: Passwords runs at most a fixed number of them
// at once (DefaultConcurrency), so that a login flood cannot take every CPU
// from the other requests. The others wait for a slot, as long as their
// context allows.
type Passwords struct {
	cost int
	// dummy is verified against when a user has no hash (unknown user,
	// NULL column), so that the time taken does not reveal it.
	dummy []byte
	slots chan struct{}
}

// DefaultConcurrency is how many bcrypt operations run at once by default:
// a quarter of the CPUs Go uses, at least one.
func DefaultConcurrency() int { return max(1, runtime.GOMAXPROCS(0)/4) }

// NewPasswords returns a Passwords hashing at cost (DefaultCost in
// production; bcrypt.MinCost keeps tests fast) with DefaultConcurrency.
func NewPasswords(cost int) (*Passwords, error) {
	return NewPasswordsLimit(cost, DefaultConcurrency())
}

// dummyHashDefaultCost is the hash of "dummy-password" at DefaultCost.
// Computing it would add a bcrypt operation (about 300 ms) to every start;
// it only has to cost what a real check costs.
const dummyHashDefaultCost = "$2a$12$q5UDaUydpm0QBcyqGeWYleh6xqVBkQIrANGpDH/g9DQkCxwICiuqC"

// NewPasswordsLimit is NewPasswords running at most concurrency bcrypt
// operations at once.
func NewPasswordsLimit(cost, concurrency int) (*Passwords, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("auth: bcrypt concurrency %d must be at least 1", concurrency)
	}
	var dummy []byte
	if cost == DefaultCost {
		dummy = []byte(dummyHashDefaultCost)
	} else {
		var err error
		if dummy, err = bcrypt.GenerateFromPassword([]byte("dummy-password"), cost); err != nil {
			return nil, fmt.Errorf("auth: bcrypt cost %d: %w", cost, err)
		}
	}
	return &Passwords{cost: cost, dummy: dummy, slots: make(chan struct{}, concurrency)}, nil
}

// acquire waits for a bcrypt slot.
func (p *Passwords) acquire(ctx context.Context) (release func(), err error) {
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	default:
	}
	select {
	case p.slots <- struct{}{}:
		return func() { <-p.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: %w", ErrBusy, ctx.Err())
	}
}

// Hash is HashContext without a deadline.
func (p *Passwords) Hash(password string) (string, error) {
	return p.HashContext(context.Background(), password)
}

// HashContext returns the bcrypt hash of password in 2.0's format
// ("$2b$12$..."). It fails with ErrBusy when ctx ends before a slot is free.
func (p *Passwords) HashContext(ctx context.Context, password string) (string, error) {
	if len(password) > MaxPasswordBytes {
		return "", ErrPasswordTooLong
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()
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

// Verify is VerifyContext without a deadline.
func (p *Passwords) Verify(password, hash string) bool {
	ok, _ := p.VerifyContext(context.Background(), password, hash)
	return ok
}

// VerifyContext reports whether password matches hash. An empty hash
// (unknown user or no password) never matches but costs the same time as a
// real check. A password over MaxPasswordBytes or a malformed hash never
// matches. It fails with ErrBusy when ctx ends before a slot is free.
func (p *Passwords) VerifyContext(ctx context.Context, password, hash string) (bool, error) {
	if len(password) > MaxPasswordBytes {
		return false, nil
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(p.dummy, []byte(password))
		return false, nil
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil, nil
}
