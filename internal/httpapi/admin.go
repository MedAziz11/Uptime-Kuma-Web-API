package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// AdminStore is what EnsureAdmin needs; *store.Store implements it.
type AdminStore interface {
	User(ctx context.Context, username string) (store.User, error)
	CreateUser(ctx context.Context, username, passwordHash string) (store.User, error)
	SetPasswordHash(ctx context.Context, username, passwordHash string) error
}

// AdminResult says what EnsureAdmin did.
type AdminResult int

// Outcomes of EnsureAdmin.
const (
	AdminUnchanged AdminResult = iota
	AdminCreated
	AdminPasswordUpdated
)

// EnsureAdmin creates the admin user, or sets its password to password when
// it does not match, as 2.0 does at every start (ADMIN_PASSWORD).
func EnsureAdmin(ctx context.Context, users AdminStore, passwords *auth.Passwords, password string) (AdminResult, error) {
	admin, err := users.User(ctx, AdminUsername)
	switch {
	case errors.Is(err, store.ErrNotFound):
		hash, err := passwords.Hash(password)
		if err != nil {
			return 0, fmt.Errorf("hash ADMIN_PASSWORD: %w", err)
		}
		if _, err := users.CreateUser(ctx, AdminUsername, hash); err != nil {
			return 0, fmt.Errorf("create the %s user: %w", AdminUsername, err)
		}
		return AdminCreated, nil
	case err != nil:
		return 0, fmt.Errorf("read the %s user: %w", AdminUsername, err)
	case passwords.Verify(password, admin.PasswordHash):
		return AdminUnchanged, nil
	}
	hash, err := passwords.Hash(password)
	if err != nil {
		return 0, fmt.Errorf("hash ADMIN_PASSWORD: %w", err)
	}
	if err := users.SetPasswordHash(ctx, AdminUsername, hash); err != nil {
		return 0, fmt.Errorf("update the %s password: %w", AdminUsername, err)
	}
	return AdminPasswordUpdated, nil
}
