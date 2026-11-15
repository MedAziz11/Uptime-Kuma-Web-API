package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var update = flag.Bool("update", false, "rewrite testdata/go_tokens.json")

func passwords(t *testing.T) *Passwords {
	t.Helper()
	p, err := NewPasswords(bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPasswordRoundTrip(t *testing.T) {
	p := passwords(t)
	for _, pw := range []string{"correct horse", "pässwörd-密码", strings.Repeat("x", MaxPasswordBytes)} {
		h, err := p.Hash(pw)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(h, "$2b$04$") {
			t.Fatalf("hash %q, want 2.0's $2b$ format", h)
		}
		if h2, _ := p.Hash(pw); h2 == h {
			t.Fatal("not salted")
		}
		if !p.Verify(pw, h) || p.Verify(pw+"x", h) || p.Verify("wrong", h) {
			t.Fatalf("verify %q", pw)
		}
	}
	if _, err := p.Hash(strings.Repeat("x", MaxPasswordBytes+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Fatalf("73 bytes: %v", err)
	}
	if _, err := NewPasswords(bcrypt.MaxCost + 1); err == nil {
		t.Fatal("bad cost accepted")
	}
}

func TestOverlongPasswordNeverVerifies(t *testing.T) {
	p := passwords(t)
	h, _ := p.Hash(strings.Repeat("x", MaxPasswordBytes))
	if p.Verify(strings.Repeat("x", MaxPasswordBytes+1), h) {
		t.Fatal("73-byte password verified against the 72-byte hash")
	}
}

func TestMissingOrMalformedHashNeverVerifies(t *testing.T) {
	p := passwords(t)
	for _, h := range []string{"", "not-a-bcrypt-hash", "$2b$12$tooshort", "$2b$04$" + strings.Repeat("A", 53)} {
		if p.Verify("anything", h) {
			t.Fatalf("verified against %q", h)
		}
		if p.Verify("dummy-password", h) {
			t.Fatalf("dummy password verified against %q", h)
		}
	}
}

// Hashes written by 2.0 (Python bcrypt 5, cost 12) and by 1.x (passlib).
func TestPythonHashesVerify(t *testing.T) {
	var fixtures struct {
		V1 struct {
			Users []struct{ Username, Password string }
		} `json:"v1"`
		V20 struct {
			Users []struct{ Username, Password string }
		} `json:"v2.0"`
	}
	b, err := os.ReadFile("../store/testdata/users.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &fixtures); err != nil {
		t.Fatal(err)
	}
	// Python bcrypt hashes of the fixture passwords (from the fixture
	// databases; reading them through the store is tested there).
	hashes := map[string]string{
		"pässwörd-密码":     "$2b$12$1zHeFuS.cgodyLV/debx..FSTdtx2PLgKT/jew9MWBcid.Dm7Uf22",
		"legacy-password": "$2b$12$SDyqfINUtOmaqjEMQdHsvePQDgIo..7zmCCrXppxhY.Vpr1up9J.O",
	}
	// One check per hash: cost 12 takes seconds under the race detector, and
	// wrong passwords are tested at MinCost above.
	p := passwords(t)
	for pw, h := range hashes {
		if !p.Verify(pw, h) {
			t.Fatalf("%q against %q", pw, h)
		}
	}
	if len(fixtures.V20.Users) == 0 || len(fixtures.V1.Users) == 0 {
		t.Fatal("fixtures not read")
	}
}

// The dummy check costs as much as a real one, so an unknown username is
// not told apart by timing.
func TestUnknownUserTakesAsLongAsAWrongPassword(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	p, err := NewPasswords(8)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := p.Hash("real-password")
	measure := func(hash string) time.Duration {
		start := time.Now()
		for range 3 {
			p.Verify("wrong-password", hash)
		}
		return time.Since(start)
	}
	known, unknown := measure(h), measure("")
	ratio := float64(unknown) / float64(known)
	if ratio < 0.5 || ratio > 2 {
		t.Fatalf("unknown user %v, wrong password %v", unknown, known)
	}
}

type vectors struct {
	Key    string `json:"key"`
	Issued []struct {
		Sub   string `json:"sub"`
		Iat   int64  `json:"iat"`
		Exp   int64  `json:"exp"`
		Token string `json:"token"`
	} `json:"issued"`
	Decoded []struct {
		Name    string  `json:"name"`
		Token   string  `json:"token"`
		Subject *string `json:"subject"`
	} `json:"decoded"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	b, err := os.ReadFile("testdata/jwt_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Issued) == 0 || len(v.Decoded) == 0 {
		t.Fatal("no vectors")
	}
	return v
}

func newTokens(t *testing.T, key string, ttl time.Duration, now time.Time) *Tokens {
	t.Helper()
	tk, err := NewTokens(key, ttl)
	if err != nil {
		t.Fatal(err)
	}
	tk.now = func() time.Time { return now }
	return tk
}

// Go issues the same bytes as PyJWT for the same claims.
func TestTokensMatchPyJWTByteForByte(t *testing.T) {
	v := loadVectors(t)
	for _, iv := range v.Issued {
		tk := newTokens(t, v.Key, time.Duration(iv.Exp-iv.Iat)*time.Second, time.Unix(iv.Iat, 999_999_999))
		if got := tk.Issue(iv.Sub); got != iv.Token {
			t.Errorf("sub %q:\n got %s\nwant %s", iv.Sub, got, iv.Token)
		}
	}
}

// Go accepts exactly the tokens 2.0 (PyJWT + decode_access_token) accepts.
func TestPyJWTTokensVerify(t *testing.T) {
	v := loadVectors(t)
	tk := newTokens(t, v.Key, time.Hour, time.Unix(1800000000, 0))
	for _, dv := range v.Decoded {
		got, err := tk.Subject(dv.Token)
		switch {
		case dv.Subject == nil && err == nil:
			t.Errorf("%s: accepted (subject %q), 2.0 rejects it", dv.Name, got)
		case dv.Subject == nil && !errors.Is(err, ErrInvalidToken):
			t.Errorf("%s: error %v does not wrap ErrInvalidToken", dv.Name, err)
		case dv.Subject != nil && err != nil:
			t.Errorf("%s: rejected (%v), 2.0 accepts it", dv.Name, err)
		case dv.Subject != nil && got != *dv.Subject:
			t.Errorf("%s: subject %q, want %q", dv.Name, got, *dv.Subject)
		}
	}
}

type goTokens struct {
	Key    string `json:"key"`
	Tokens []struct {
		Sub        string `json:"sub"`
		TTLSeconds int64  `json:"ttl_seconds"`
		Token      string `json:"token"`
	} `json:"tokens"`
}

// testdata/go_tokens.json holds tokens issued by this package; the Python
// script (gen_jwt_vectors.py --check) verifies them with PyJWT and 2.0's
// decode_access_token. This test keeps the file in sync with the code.
func TestGoTokensVerifyWithPyJWT(t *testing.T) {
	const key = "go-minted-key-go-minted-key-go-minted-key"
	doc := goTokens{Key: key}
	now := time.Unix(1794580000, 0)
	for _, c := range []struct {
		sub string
		ttl time.Duration
	}{
		{"alice", 100 * 365 * 24 * time.Hour},
		{"admin", 100 * 365 * 24 * time.Hour},
		{"élise", 50 * 365 * 24 * time.Hour},
		{"g.h-i_j", 11520 * time.Minute * 1000},
	} {
		tk := newTokens(t, key, c.ttl, now)
		doc.Tokens = append(doc.Tokens, struct {
			Sub        string `json:"sub"`
			TTLSeconds int64  `json:"ttl_seconds"`
			Token      string `json:"token"`
		}{c.sub, int64(c.ttl / time.Second), tk.Issue(c.sub)})
	}
	want, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	path := filepath.Join("testdata", "go_tokens.json")
	if *update {
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is out of date: run go test -run TestGoTokensVerifyWithPyJWT -update, then gen_jwt_vectors.py --check", path)
	}
	// And they verify here, now and until they expire.
	for _, tok := range doc.Tokens {
		tk := newTokens(t, key, time.Hour, now)
		if sub, err := tk.Subject(tok.Token); err != nil || sub != tok.Sub {
			t.Fatalf("%q: %q %v", tok.Sub, sub, err)
		}
	}
}

func TestTokenRoundTripAndExpiry(t *testing.T) {
	now := time.Now()
	tk := newTokens(t, "unit-test-signing-key", 5*time.Minute, now)
	token := tk.Issue("alice")
	if sub, err := tk.Subject(token); err != nil || sub != "alice" {
		t.Fatalf("%q %v", sub, err)
	}
	var claims map[string]any
	_, payload, _ := strings.Cut(token, ".")
	payload, _, _ = strings.Cut(payload, ".")
	b, _ := decodeSegment(payload)
	if err := json.Unmarshal(b, &claims); err != nil || len(claims) != 3 || claims["exp"].(float64)-claims["iat"].(float64) != 300 {
		t.Fatalf("claims %v %v", claims, err)
	}

	// Valid until exp, rejected from exp on (PyJWT: exp <= now is expired).
	tk.now = func() time.Time { return now.Add(5*time.Minute - time.Second) }
	if _, err := tk.Subject(token); err != nil {
		t.Fatal(err)
	}
	tk.now = func() time.Time { return time.Unix(now.Add(5*time.Minute).Unix(), 0) }
	if _, err := tk.Subject(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("at exp: %v", err)
	}

	other := newTokens(t, "another-key", time.Hour, now)
	if _, err := other.Subject(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("verified with another key")
	}
}

func TestNewTokensRejectsBadArguments(t *testing.T) {
	if _, err := NewTokens("", time.Hour); err == nil {
		t.Fatal("empty key")
	}
	if _, err := NewTokens("k", 0); err == nil {
		t.Fatal("zero ttl")
	}
}

func FuzzSubject(f *testing.F) {
	tk, _ := NewTokens("fuzz-key", time.Hour)
	f.Add(tk.Issue("alice"))
	f.Add("a.b.c")
	f.Add("")
	f.Fuzz(func(t *testing.T, token string) {
		sub, err := tk.Subject(token)
		if err == nil && sub == "" {
			t.Fatal("empty subject accepted")
		}
	})
}

func TestResolveSecretKey(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "db", "db.sqlite3")

	if key, gen, err := ResolveSecretKey("explicit-key", db); err != nil || key != "explicit-key" || gen {
		t.Fatalf("%q %v %v", key, gen, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "db")); !os.IsNotExist(err) {
		t.Fatal("explicit key created the db directory")
	}

	key, gen, err := ResolveSecretKey("", db)
	if err != nil || !gen || len(key) != 64 {
		t.Fatalf("%q %v %v", key, gen, err)
	}
	file := filepath.Join(dir, "db", SecretKeyFile)
	b, err := os.ReadFile(file)
	if err != nil || string(b) != key {
		t.Fatalf("file %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v", st.Mode())
		}
	}
	if again, gen, err := ResolveSecretKey("", db); err != nil || again != key || gen {
		t.Fatalf("second resolve %q %v %v", again, gen, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "db"))
	if len(entries) != 1 {
		t.Fatalf("left files behind: %v", entries)
	}

	// A key file written by 2.0 (or by hand) is read and trimmed.
	if err := os.WriteFile(file, []byte("  key-from-2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, _, _ := ResolveSecretKey("", db); k != "key-from-2.0" {
		t.Fatalf("got %q", k)
	}
	// A blank file is replaced.
	if err := os.WriteFile(file, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if k, gen, _ := ResolveSecretKey("", db); k == "" || !gen {
		t.Fatalf("blank file: %q %v", k, gen)
	}
	// The key file generated by 2.0 for the upgrade fixture.
	if k, gen, err := ResolveSecretKey("", "../store/testdata/v2.0/db.sqlite3"); err != nil || gen || len(k) != 64 {
		t.Fatalf("2.0 key file: %v %v %v", len(k), gen, err)
	}
}

func TestResolveSecretKeyErrors(t *testing.T) {
	dir := t.TempDir()
	// The key path is a directory: it cannot be read.
	if err := os.MkdirAll(filepath.Join(dir, SecretKeyFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveSecretKey("", filepath.Join(dir, "db.sqlite3")); err == nil {
		t.Fatal("no error")
	}
	// The database directory cannot be created: a file is in the way.
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ResolveSecretKey("", filepath.Join(blocker, "db", "db.sqlite3")); err == nil {
		t.Fatal("no error")
	}
}

// Python: json.dumps(s) (ensure_ascii=True).
func TestPythonJSONString(t *testing.T) {
	got := string(appendPythonJSONString(nil, "a\b\f\r\x01\x7f \"\\é😀"))
	want := strings.ReplaceAll(`"a|b|f|r|u0001|u007f|u2028|"|||u00e9|ud83d|ude00"`, "|", `\`)
	if got != want {
		t.Fatalf("%s, want %s", got, want)
	}
}

func TestIsTruthy(t *testing.T) {
	for raw, want := range map[string]bool{
		`null`: false, `false`: false, `true`: true, `0`: false, `0.0`: false, `1`: true,
		`""`: false, `"x"`: true, `[]`: false, `[0]`: true, `{}`: false, `{"a":1}`: true, `nope`: true,
	} {
		if got := isTruthy(json.RawMessage(raw)); got != want {
			t.Errorf("%s: %v", raw, got)
		}
	}
}

func TestMalformedTokens(t *testing.T) {
	tk := newTokens(t, "k", time.Hour, time.Unix(1700000000, 0))
	good := tk.Issue("alice")
	h, rest, _ := strings.Cut(good, ".")
	p, s, _ := strings.Cut(rest, ".")
	sign := func(header, payload string) string {
		in := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString([]byte(payload))
		return in + "." + base64.RawURLEncoding.EncodeToString(tk.mac(in))
	}
	for name, tok := range map[string]string{
		"no dot":            "abc",
		"bad header b64":    "!!." + p + "." + s,
		"bad signature b64": h + "." + p + ".!!",
		"bad payload b64":   sign(`{"alg":"HS256"}`, "x")[:0] + h + ".!!." + s,
		"b64 false":         sign(`{"alg":"HS256","b64":false}`, `{"sub":"a","exp":1800000000}`),
		"nbf string":        sign(`{"alg":"HS256"}`, `{"sub":"a","exp":1800000000,"nbf":"x"}`),
	} {
		if _, err := tk.Subject(tok); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if sub, err := tk.Subject(sign(`{"alg":"HS256","b64":true}`, `{"sub":"a","exp":1e300}`)); err != nil || sub != "a" {
		t.Fatalf("b64 true / far exp: %q %v", sub, err)
	}
}

// Verified tokens are cached; the cache keeps every guarantee of a full
// verification.
func TestVerifiedTokenCache(t *testing.T) {
	now := time.Unix(1794580000, 250_000_000)
	tk := newTokens(t, "cache-signing-key", time.Minute, now)
	tok := tk.Issue("alice")
	for range 2 {
		if sub, err := tk.Subject(tok); err != nil || sub != "alice" {
			t.Fatalf("%q %v", sub, err)
		}
	}
	if len(tk.verified) != 1 {
		t.Fatalf("%d cached", len(tk.verified))
	}
	// Expiry is checked on every hit, to the same precision: valid just
	// before exp, rejected at exp.
	exp := time.Unix(now.Add(time.Minute).Unix(), 0)
	tk.now = func() time.Time { return exp.Add(-time.Nanosecond * 1000) }
	if _, err := tk.Subject(tok); err != nil {
		t.Fatalf("before exp: %v", err)
	}
	tk.now = func() time.Time { return exp }
	if _, err := tk.Subject(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("at exp: %v", err)
	}
	if len(tk.verified) != 0 {
		t.Fatal("expired token kept")
	}
	// Another key never accepts it, cached here or not.
	tk.now = func() time.Time { return now }
	tk.Subject(tok)
	other := newTokens(t, "another-signing-key", time.Minute, now)
	if _, err := other.Subject(tok); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("other key: %v", err)
	}
	// Invalid tokens are not cached; the cache is bounded.
	tk.Subject(tok + "x")
	tk.Subject("garbage")
	if len(tk.verified) != 1 {
		t.Fatalf("%d cached", len(tk.verified))
	}
	for i := range maxVerified + 10 {
		if _, err := tk.Subject(tk.Issue(fmt.Sprint("u", i))); err != nil {
			t.Fatal(err)
		}
	}
	if len(tk.verified) != maxVerified {
		t.Fatalf("%d cached", len(tk.verified))
	}
}

func BenchmarkSubject(b *testing.B) {
	tk, _ := NewTokens("bench-signing-key", time.Hour)
	tok := tk.Issue("admin")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := tk.Subject(tok); err != nil {
			b.Fatal(err)
		}
	}
}

// At most the configured number of bcrypt operations run at once; the
// others wait for a slot as long as their context allows.
func TestBcryptConcurrencyIsBounded(t *testing.T) {
	p, err := NewPasswordsLimit(bcrypt.MinCost, 2)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := p.Hash("password")
	// Hold both slots.
	r1, _ := p.acquire(context.Background())
	r2, _ := p.acquire(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := p.VerifyContext(ctx, "password", h); !errors.Is(err, ErrBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("verify without a slot: %v", err)
	}
	if _, err := p.HashContext(ctx, "password"); !errors.Is(err, ErrBusy) {
		t.Fatalf("hash without a slot: %v", err)
	}
	// A waiter gets the slot that is released.
	done := make(chan bool)
	go func() { done <- p.Verify("password", h) }()
	select {
	case <-done:
		t.Fatal("ran without a slot")
	case <-time.After(20 * time.Millisecond):
	}
	r1()
	if !<-done {
		t.Fatal("verify failed")
	}
	r2()
	if len(p.slots) != 0 {
		t.Fatalf("%d slots held", len(p.slots))
	}
	if _, err := NewPasswordsLimit(bcrypt.MinCost, 0); err == nil {
		t.Fatal("no slot accepted")
	}
	if DefaultConcurrency() < 1 {
		t.Fatal("default concurrency")
	}
}

// The precomputed dummy hash costs what a real check at DefaultCost costs.
func TestDummyHashOfDefaultCost(t *testing.T) {
	if cost, err := bcrypt.Cost([]byte(dummyHashDefaultCost)); err != nil || cost != DefaultCost {
		t.Fatalf("cost %d: %v", cost, err)
	}
	p, err := NewPasswordsLimit(DefaultCost, 1)
	if err != nil || string(p.dummy) != dummyHashDefaultCost {
		t.Fatal(err)
	}
}
