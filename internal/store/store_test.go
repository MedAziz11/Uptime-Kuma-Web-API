package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// copyFixture copies a committed database into a temporary directory, so
// tests never modify testdata.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "db", "db.sqlite3")
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		t.Fatal(err)
	}
	src, err := os.Open(filepath.Join("testdata", name, "db.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, src); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestFreshDatabaseHas2_0Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "db", "db.sqlite3")
	s := open(t, path)
	ctx := context.Background()

	var ddl, mode string
	if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'users'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(schema, "CREATE TABLE IF NOT EXISTS", "CREATE TABLE", 1)
	if ddl != want {
		t.Fatalf("schema\n%s\nwant 2.0's\n%s", ddl, want)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode %q %v", mode, err)
	}
	if users, err := s.Users(ctx); err != nil || len(users) != 0 {
		t.Fatalf("%v %v", users, err)
	}
}

func TestUserLifecycle(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite3"))
	ctx := context.Background()
	clock := time.Date(2026, 11, 13, 15, 43, 41, 808921500, time.UTC)
	s.now = func() time.Time { return clock }

	alice, err := s.CreateUser(ctx, "alice", "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if alice.ID != 1 || !alice.CreatedAt.Equal(clock.Truncate(time.Microsecond)) || alice.LastVisit != alice.CreatedAt {
		t.Fatalf("%+v", alice)
	}
	if _, err := s.CreateUser(ctx, "alice", "other"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := s.CreateUser(ctx, "nohash", ""); err != nil {
		t.Fatal(err)
	}

	got, err := s.User(ctx, "alice")
	if err != nil || got != alice {
		t.Fatalf("%+v %v, want %+v", got, err, alice)
	}
	if _, err := s.User(ctx, "Alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("usernames are case-sensitive: %v", err)
	}
	if u, _ := s.User(ctx, "nohash"); u.PasswordHash != "" {
		t.Fatalf("NULL hash read as %q", u.PasswordHash)
	}

	// Raw row format: as 2.0 writes it.
	var created string
	if err := s.db.QueryRowContext(ctx, `SELECT CAST(created_at AS TEXT) || ' ' || typeof(created_at) FROM users WHERE username='alice'`).Scan(&created); err != nil || created != "2026-11-13 15:43:41.808921+00:00 text" {
		t.Fatalf("created_at stored as %q %v", created, err)
	}

	clock = clock.Add(time.Hour)
	if err := s.TouchLastVisit(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.User(ctx, "alice"); !u.LastVisit.Equal(clock.Truncate(time.Microsecond)) || !u.CreatedAt.Equal(alice.CreatedAt) {
		t.Fatalf("after touch %+v", u)
	}
	if err := s.SetPasswordHash(ctx, "alice", "hash-b"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.User(ctx, "alice"); u.PasswordHash != "hash-b" {
		t.Fatalf("hash %q", u.PasswordHash)
	}

	users, err := s.Users(ctx)
	if err != nil || len(users) != 2 || users[0].Username != "alice" || users[1].Username != "nohash" {
		t.Fatalf("%+v %v", users, err)
	}
	users[0].Username = "mutated"
	if u, _ := s.User(ctx, "alice"); u.Username != "alice" {
		t.Fatal("Users returned the cache itself")
	}

	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted user still found: %v", err)
	}
	for _, err := range []error{
		s.DeleteUser(ctx, "alice"),
		s.TouchLastVisit(ctx, "alice"),
		s.SetPasswordHash(ctx, "alice", "x"),
	} {
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing user: %v", err)
		}
	}
	// AUTOINCREMENT: ids are not reused.
	if u, _ := s.CreateUser(ctx, "carol", "h"); u.ID != 3 {
		t.Fatalf("id %d", u.ID)
	}
}

// The per-request lookup is served from memory: no query after the first.
func TestReadsAreCached(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite3"))
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "alice", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	// Change the row behind the store's back: the cache still answers.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, "alice"); err != nil {
		t.Fatalf("lookup hit SQLite: %v", err)
	}
	// Any write through the store refreshes it.
	if _, err := s.CreateUser(ctx, "bob", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.User(ctx, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cache not invalidated: %v", err)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite3"))
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			name := fmt.Sprintf("user%d", i)
			for range 20 {
				if _, err := s.CreateUser(ctx, name, "h"); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.User(ctx, name); err != nil {
					t.Error(err)
					return
				}
				if err := s.DeleteUser(ctx, name); err != nil {
					t.Error(err)
					return
				}
				if _, err := s.User(ctx, name); !errors.Is(err, ErrNotFound) {
					t.Errorf("%s visible after delete: %v", name, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestCanceledContext(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite3"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.User(ctx, "x"); err == nil {
		t.Fatal("no error")
	}
	if _, err := s.CreateUser(ctx, "x", "h"); err == nil {
		t.Fatal("no error")
	}
	if err := s.DeleteUser(ctx, "x"); err == nil {
		t.Fatal("no error")
	}
	// A load already waiting gives up with the context.
	s.load <- struct{}{}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel2()
	if _, err := s.Users(ctx2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	<-s.load
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Open(ctx, ""); err == nil {
		t.Fatal("empty path")
	}
	if _, err := Open(ctx, "db?mode=ro"); err == nil {
		t.Fatal("query in path")
	}
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	os.WriteFile(blocker, nil, 0o600)
	if _, err := Open(ctx, filepath.Join(blocker, "db.sqlite3")); err == nil {
		t.Fatal("directory under a file")
	}
	// A directory where the database should be.
	if _, err := Open(ctx, dir); err == nil {
		t.Fatal("directory as database")
	}
	// A users table that is not 2.0's.
	other := filepath.Join(dir, "other.sqlite3")
	db, err := sql.Open("sqlite", other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE users (name TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(ctx, other); err == nil || !strings.Contains(err.Error(), "not the 1.x/2.0 table") {
		t.Fatalf("got %v", err)
	}
}

func TestBadTimestampIsAnError(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "db.sqlite3"))
	ctx := context.Background()
	for col, values := range map[string]string{
		"created_at": `'yesterday', '2026-01-01 00:00:00'`,
		"last_visit": `'2026-01-01 00:00:00', 'yesterday'`,
	} {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM users`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO users (username, created_at, last_visit) VALUES ('x', `+values+`)`); err != nil {
			t.Fatal(err)
		}
		s.invalidate()
		if _, err := s.User(ctx, "x"); err == nil || !strings.Contains(err.Error(), col) {
			t.Fatalf("%s: %v", col, err)
		}
	}
}

func TestTimestamps(t *testing.T) {
	for in, want := range map[string]string{
		"2026-11-13 15:43:41.808921+00:00": "2026-11-13T15:43:41.808921Z", // 2.0
		"2026-11-13 15:43:41+00:00":        "2026-11-13T15:43:41Z",        // 2.0, whole second
		"2023-04-11 10:00:00.123456":       "2023-04-11T10:00:00.123456Z", // 1.x, naive = UTC
		"2023-04-11 10:00:00":              "2023-04-11T10:00:00Z",        // CURRENT_TIMESTAMP
		"2023-04-11T12:00:00+02:00":        "2023-04-11T10:00:00Z",
		"2023-04-11T10:00:00Z":             "2023-04-11T10:00:00Z",
	} {
		got, err := parseTimestamp(in)
		if err != nil || got.Format(time.RFC3339Nano) != want {
			t.Errorf("%q: %v %v, want %s", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "2023-04-11", "1681207200"} {
		if _, err := parseTimestamp(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	for in, want := range map[time.Time]string{
		time.Date(2026, 1, 2, 3, 4, 5, 6000, time.UTC):              "2026-01-02 03:04:05.000006+00:00",
		time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC):                 "2026-01-02 03:04:05+00:00",
		time.Date(2026, 1, 2, 5, 4, 5, 0, time.FixedZone("", 7200)): "2026-01-02 03:04:05+00:00",
	} {
		if got := formatTimestamp(in); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
}

type fixtureUsers struct {
	Users []struct{ Username, Password string } `json:"users"`
}

func readFixtureUsers(t *testing.T) (v1, v20 fixtureUsers) {
	t.Helper()
	b, err := os.ReadFile("testdata/users.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]fixtureUsers
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc["v1"], doc["v2.0"]
}

// Databases written by 1.x and by the 2.0 application open unchanged, their
// users and timestamps read back and their Python bcrypt hashes verify.
func TestUpgradeFrom1xAnd2_0(t *testing.T) {
	v1, v20 := readFixtureUsers(t)
	ctx := context.Background()
	for _, tc := range []struct {
		dir     string
		users   fixtureUsers
		ids     []int64
		created string
	}{
		{"v1", v1, []int64{1, 2}, "2023-04-11T10:00:00.123456Z"},
		{"v2.0", v20, []int64{1, 2, 4}, ""},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			s := open(t, copyFixture(t, tc.dir))
			users, err := s.Users(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(users) != len(tc.ids) {
				t.Fatalf("%d users", len(users))
			}
			for i, u := range users {
				if u.ID != tc.ids[i] || u.Username != tc.users.Users[i].Username {
					t.Fatalf("user %d: %+v", i, u)
				}
				if tc.created != "" && u.CreatedAt.Format(time.RFC3339Nano) != tc.created {
					t.Fatalf("created_at %v", u.CreatedAt)
				}
				if u.CreatedAt.IsZero() || u.LastVisit.Before(u.CreatedAt) {
					t.Fatalf("timestamps %+v", u)
				}
			}
			// One bcrypt check per database (cost 12 is slow under -race).
			last := tc.users.Users[len(tc.users.Users)-1]
			u, err := s.User(ctx, last.Username)
			if err != nil {
				t.Fatal(err)
			}
			if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(last.Password)) != nil {
				t.Fatalf("%s: Python hash does not verify", last.Username)
			}
			// Writes keep working, in the same format.
			if _, err := s.CreateUser(ctx, "newcomer", "h"); err != nil {
				t.Fatal(err)
			}
			if err := s.TouchLastVisit(ctx, users[0].Username); err != nil {
				t.Fatal(err)
			}
		})
	}
}
