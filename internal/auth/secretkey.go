package auth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SecretKeyFile is the name of the generated key file, next to the database.
const SecretKeyFile = "secret_key"

// ResolveSecretKey returns the token signing key as 2.0 does: fromEnv when it
// is set (SECRET_KEY), else the key stored in <dir of dbPath>/secret_key,
// else a new key (48 random bytes, unpadded base64url, like Python's
// secrets.token_urlsafe(48)) written to that file with mode 0600. generated
// reports that a new key was written. The key is never part of an error.
func ResolveSecretKey(fromEnv, dbPath string) (key string, generated bool, err error) {
	if fromEnv != "" {
		return fromEnv, false, nil
	}
	dir := filepath.Dir(dbPath)
	path := filepath.Join(dir, SecretKeyFile)

	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if key := strings.TrimSpace(string(b)); key != "" {
			return key, false, nil
		}
	case !errors.Is(err, fs.ErrNotExist):
		return "", false, fmt.Errorf("auth: read secret key: %w", err)
	}

	raw := make([]byte, 48)
	if _, err := rand.Read(raw); err != nil {
		return "", false, fmt.Errorf("auth: generate secret key: %w", err)
	}
	key = base64.RawURLEncoding.EncodeToString(raw)

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", false, fmt.Errorf("auth: create %s: %w", dir, err)
	}
	// Written to a temporary file (created 0600) and renamed, so that a
	// crash never leaves a truncated key behind.
	tmp, err := os.CreateTemp(dir, SecretKeyFile+".*.tmp")
	if err != nil {
		return "", false, fmt.Errorf("auth: write secret key: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if _, err := tmp.WriteString(key); err != nil {
		tmp.Close()
		return "", false, fmt.Errorf("auth: write secret key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", false, fmt.Errorf("auth: write secret key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", false, fmt.Errorf("auth: write secret key: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", false, fmt.Errorf("auth: write secret key: %w", err)
	}
	return key, true, nil
}
