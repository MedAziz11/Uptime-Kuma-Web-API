// Package store keeps the API users in SQLite.
//
// The database is the one 1.x and 2.0 use: the "users" table created by
// tortoise-orm, read and written unchanged (timestamps as text, in the
// format Python's str(datetime) produces). A fresh database gets the same
// schema.
//
// SQLite settings: WAL journal (2.0's tortoise-orm already switches the
// file to WAL, and the mode is persistent; readers never block the writer;
// it needs a local filesystem, as 2.0 did), a 5 s busy timeout for other
// processes holding a lock (e.g. a sqlite3 shell), immediate write
// transactions, and a single connection, so this process is always a single
// writer and never contends with itself.
//
// Reads are served from an in-memory copy of the table, rebuilt on the
// first read after any write, so authenticating a request does not query
// SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Errors returned by Store; test with errors.Is.
var (
	ErrNotFound = errors.New("store: user not found")
	ErrExists   = errors.New("store: username already exists")
)

// User is a row of the users table.
type User struct {
	ID       int64
	Username string
	// PasswordHash is the bcrypt hash; "" when the column is NULL (a user
	// who cannot log in).
	PasswordHash string
	CreatedAt    time.Time // UTC
	LastVisit    time.Time // UTC
}

// schema is the DDL tortoise-orm (2.0) generates for app.models.Users.
const schema = `CREATE TABLE IF NOT EXISTS "users" (
    "id" INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL,
    "username" VARCHAR(20) NOT NULL UNIQUE,
    "password_hash" VARCHAR(200),
    "created_at" TIMESTAMP NOT NULL,
    "last_visit" TIMESTAMP NOT NULL
)`

// The timestamps are read as text: the driver would otherwise convert
// TIMESTAMP columns itself.
const selectUsers = `SELECT id, username, password_hash, CAST(created_at AS TEXT), CAST(last_visit AS TEXT) FROM users ORDER BY id`

// Store is the users table. It is safe for concurrent use.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu   sync.RWMutex
	snap *snapshot // nil: reload on the next read
	gen  uint64    // incremented by every write
	load chan struct{}
}

type snapshot struct {
	byName map[string]User
	byID   []User // ordered by id
}

// Open opens (or creates) the database at path, creating its directory and
// the users table if needed, and checks that an existing table has the
// expected columns.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("store: unsupported database path %q", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("store: create database directory: %w", err)
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)

	s := &Store{db: db, now: time.Now, load: make(chan struct{}, 1)}
	if err := s.init(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: %s: %w", path, err)
	}
	return s, nil
}

func (s *Store) init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create users table: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, selectUsers+" LIMIT 0")
	if err != nil {
		return fmt.Errorf("users table is not the 1.x/2.0 table: %w", err)
	}
	return rows.Close()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// User returns the user named username, or ErrNotFound.
func (s *Store) User(ctx context.Context, username string) (User, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return User{}, err
	}
	u, ok := snap.byName[username]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// Users returns every user, ordered by id.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	snap, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return append([]User(nil), snap.byID...), nil
}

// CreateUser inserts a user and returns it; ErrExists if the username is
// taken.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	now := s.now().UTC().Truncate(time.Microsecond)
	ts := formatTimestamp(now)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, created_at, last_visit) VALUES (?, ?, ?, ?)`,
		username, nullable(passwordHash), ts, ts)
	s.invalidate()
	if err != nil {
		var se *sqlite.Error
		if errors.As(err, &se) && se.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
			return User{}, ErrExists
		}
		return User{}, fmt.Errorf("store: create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, fmt.Errorf("store: create user: %w", err)
	}
	return User{ID: id, Username: username, PasswordHash: passwordHash, CreatedAt: now, LastVisit: now}, nil
}

// DeleteUser deletes a user; ErrNotFound if there is none.
func (s *Store) DeleteUser(ctx context.Context, username string) error {
	return s.update(ctx, `DELETE FROM users WHERE username = ?`, username)
}

// SetPasswordHash replaces a user's password hash.
func (s *Store) SetPasswordHash(ctx context.Context, username, passwordHash string) error {
	return s.update(ctx, `UPDATE users SET password_hash = ? WHERE username = ?`, nullable(passwordHash), username)
}

// TouchLastVisit sets a user's last_visit to now, as a 2.0 login does.
func (s *Store) TouchLastVisit(ctx context.Context, username string) error {
	ts := formatTimestamp(s.now().UTC().Truncate(time.Microsecond))
	return s.update(ctx, `UPDATE users SET last_visit = ? WHERE username = ?`, ts, username)
}

func (s *Store) update(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	s.invalidate()
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// invalidate drops the cached table after a write (also after a failed
// one: the outcome of a failed statement is not assumed).
func (s *Store) invalidate() {
	s.mu.Lock()
	s.gen++
	s.snap = nil
	s.mu.Unlock()
}

// snapshot returns the cached table, loading it if needed. Concurrent
// loads are collapsed into one. A load that overlaps a write is returned to
// its caller but not cached, so a read that starts after a write returns
// sees that write.
func (s *Store) snapshot(ctx context.Context) (*snapshot, error) {
	s.mu.RLock()
	snap := s.snap
	s.mu.RUnlock()
	if snap != nil {
		return snap, nil
	}

	select {
	case s.load <- struct{}{}:
		defer func() { <-s.load }()
	case <-ctx.Done():
		return nil, fmt.Errorf("store: %w", ctx.Err())
	}
	s.mu.RLock()
	snap, gen := s.snap, s.gen
	s.mu.RUnlock()
	if snap != nil {
		return snap, nil
	}

	snap, err := s.query(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.gen == gen {
		s.snap = snap
	}
	s.mu.Unlock()
	return snap, nil
}

func (s *Store) query(ctx context.Context) (*snapshot, error) {
	rows, err := s.db.QueryContext(ctx, selectUsers)
	if err != nil {
		return nil, fmt.Errorf("store: read users: %w", err)
	}
	defer rows.Close()
	snap := &snapshot{byName: map[string]User{}}
	for rows.Next() {
		var (
			u                  User
			hash               sql.NullString
			created, lastVisit string
		)
		if err := rows.Scan(&u.ID, &u.Username, &hash, &created, &lastVisit); err != nil {
			return nil, fmt.Errorf("store: read users: %w", err)
		}
		u.PasswordHash = hash.String
		if u.CreatedAt, err = parseTimestamp(created); err != nil {
			return nil, fmt.Errorf("store: user %d created_at: %w", u.ID, err)
		}
		if u.LastVisit, err = parseTimestamp(lastVisit); err != nil {
			return nil, fmt.Errorf("store: user %d last_visit: %w", u.ID, err)
		}
		snap.byName[u.Username] = u
		snap.byID = append(snap.byID, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: read users: %w", err)
	}
	return snap, nil
}

// formatTimestamp writes t (UTC) as 2.0 does, with Python's str(datetime):
// "2026-11-13 15:43:41.808921+00:00", without the fraction when it is zero.
func formatTimestamp(t time.Time) string {
	t = t.UTC()
	s := t.Format("2006-01-02 15:04:05")
	if us := t.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return s + "+00:00"
}

// parseTimestamp reads what 1.x, 2.0 and SQLite's CURRENT_TIMESTAMP write:
// "YYYY-MM-DD HH:MM:SS[.ffffff][+HH:MM|Z]", also with a "T" separator.
// Timestamps without an offset (1.x) are UTC, as 2.0 reads them.
func parseTimestamp(s string) (time.Time, error) {
	v := s
	if len(v) > 10 && v[10] == 'T' {
		v = v[:10] + " " + v[11:]
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
}
