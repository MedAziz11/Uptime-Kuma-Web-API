package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

var required = map[string]string{
	"KUMA_SERVER":    "http://kuma:3001",
	"KUMA_USERNAME":  "kuma-user",
	"KUMA_PASSWORD":  "kuma-password",
	"ADMIN_PASSWORD": "admin-password",
}

// env returns a LookupFunc over required plus overrides; an override "\x00"
// unsets the variable.
func env(overrides map[string]string) LookupFunc {
	vars := map[string]string{}
	for k, v := range required {
		vars[k] = v
	}
	for k, v := range overrides {
		if v == "\x00" {
			delete(vars, k)
		} else {
			vars[k] = v
		}
	}
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

func load(t *testing.T, overrides map[string]string) (*Config, *Error) {
	t.Helper()
	cfg, err := Load(env(overrides))
	if err == nil {
		return cfg, nil
	}
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("Load error %T %v, want *Error", err, err)
	}
	return nil, cerr
}

func TestDefaults(t *testing.T) {
	cfg, err := load(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		KumaServer:            "http://kuma:3001",
		KumaUsername:          "kuma-user",
		KumaPassword:          NewSecret("kuma-password"),
		KumaTimeout:           10 * time.Second,
		KumaSSLVerify:         true,
		AdminPassword:         NewSecret("admin-password"),
		AccessTokenExpiration: 11520 * time.Minute,
		DBPath:                "db/db.sqlite3",
		LogLevel:              slog.LevelInfo,
		ListenAddr:            ":8000",
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got %+v\nwant %+v", cfg, want)
	}
	if cfg.SecretKey.IsSet() {
		t.Fatal("SECRET_KEY set by default")
	}
}

func TestReadsTheEnvironment(t *testing.T) {
	cfg, err := load(t, map[string]string{
		"ACCESS_TOKEN_EXPIRATION": "5",
		"BACKEND_CORS_ORIGINS":    `["https://a.example", "https://b.example"]`,
		"KUMA_SSL_VERIFY":         "false",
		"KUMA_TIMEOUT":            "1.5",
		"SECRET_KEY":              " k ",
		"DB_PATH":                 "/db/db.sqlite3",
		"LOG_LEVEL":               "debug",
		"LISTEN_ADDR":             "127.0.0.1:9000",
		"KUMA_SERVER":             "https://kuma.example.com//",
		"UNRELATED_VARIABLE":      "ignored",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AccessTokenExpiration != 5*time.Minute || cfg.KumaSSLVerify || cfg.KumaTimeout != 1500*time.Millisecond ||
		cfg.SecretKey.Value() != " k " || cfg.DBPath != "/db/db.sqlite3" || cfg.LogLevel != slog.LevelDebug ||
		cfg.ListenAddr != "127.0.0.1:9000" || cfg.KumaServer != "https://kuma.example.com" {
		t.Fatalf("got %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.CORSOrigins, []string{"https://a.example", "https://b.example"}) {
		t.Fatalf("origins %q", cfg.CORSOrigins)
	}
}

// The cases below were checked against 2.0's Settings (pydantic-settings).
func TestValues(t *testing.T) {
	type check func(*Config) bool
	type valueCase struct {
		name, value string
		valid       bool
		check       check
	}
	ok := func(name, value string, c check) valueCase { return valueCase{name, value, true, c} }
	bad := func(name, value string) valueCase { return valueCase{name, value, false, nil} }
	timeout := func(d time.Duration) check { return func(c *Config) bool { return c.KumaTimeout == d } }
	expiry := func(m int) check {
		return func(c *Config) bool { return c.AccessTokenExpiration == time.Duration(m)*time.Minute }
	}
	verify := func(b bool) check { return func(c *Config) bool { return c.KumaSSLVerify == b } }
	level := func(l slog.Level) check { return func(c *Config) bool { return c.LogLevel == l } }
	origins := func(o ...string) check {
		return func(c *Config) bool {
			return len(c.CORSOrigins) == len(o) && fmt.Sprint(c.CORSOrigins) == fmt.Sprint(o)
		}
	}

	cases := []valueCase{
		ok("KUMA_TIMEOUT", "10", timeout(10*time.Second)),
		ok("KUMA_TIMEOUT", " 2 ", timeout(2*time.Second)),
		ok("KUMA_TIMEOUT", "1e3", timeout(1000*time.Second)),
		ok("KUMA_TIMEOUT", "1_0", timeout(10*time.Second)),
		ok("KUMA_TIMEOUT", ".5", timeout(500*time.Millisecond)),
		bad("KUMA_TIMEOUT", "0"),
		bad("KUMA_TIMEOUT", "-1"),
		bad("KUMA_TIMEOUT", "abc"),
		bad("KUMA_TIMEOUT", ""),
		bad("KUMA_TIMEOUT", "0x10"),
		bad("KUMA_TIMEOUT", "nan"),
		bad("KUMA_TIMEOUT", "inf"), // 2.0 accepts it; a timeout must be finite
		bad("KUMA_TIMEOUT", "1e300"),
		bad("KUMA_TIMEOUT", "1e-12"),
		bad("KUMA_TIMEOUT", "_1"),
		bad("KUMA_TIMEOUT", "1__0"),

		ok("KUMA_SSL_VERIFY", "TrUe", verify(true)),
		ok("KUMA_SSL_VERIFY", "yes", verify(true)),
		ok("KUMA_SSL_VERIFY", "on", verify(true)),
		ok("KUMA_SSL_VERIFY", "1", verify(true)),
		ok("KUMA_SSL_VERIFY", "y", verify(true)),
		ok("KUMA_SSL_VERIFY", "t", verify(true)),
		ok("KUMA_SSL_VERIFY", "off", verify(false)),
		ok("KUMA_SSL_VERIFY", "NO", verify(false)),
		ok("KUMA_SSL_VERIFY", "0", verify(false)),
		ok("KUMA_SSL_VERIFY", "f", verify(false)),
		ok("KUMA_SSL_VERIFY", "n", verify(false)),
		bad("KUMA_SSL_VERIFY", " true "),
		bad("KUMA_SSL_VERIFY", "2"),
		bad("KUMA_SSL_VERIFY", ""),

		ok("ACCESS_TOKEN_EXPIRATION", " 5 ", expiry(5)),
		ok("ACCESS_TOKEN_EXPIRATION", "5.0", expiry(5)),
		ok("ACCESS_TOKEN_EXPIRATION", "+5", expiry(5)),
		ok("ACCESS_TOKEN_EXPIRATION", "1_000", expiry(1000)),
		bad("ACCESS_TOKEN_EXPIRATION", "5.5"),
		bad("ACCESS_TOKEN_EXPIRATION", "0"),
		bad("ACCESS_TOKEN_EXPIRATION", "-3"),
		bad("ACCESS_TOKEN_EXPIRATION", "-99999999999999999999"),
		bad("ACCESS_TOKEN_EXPIRATION", ""),
		bad("ACCESS_TOKEN_EXPIRATION", "abc"),
		bad("ACCESS_TOKEN_EXPIRATION", "99999999999999999999"), // 2.0 accepts it, then fails on login
		bad("ACCESS_TOKEN_EXPIRATION", "153722868"),
		ok("ACCESS_TOKEN_EXPIRATION", "153722867", expiry(153722867)),

		ok("BACKEND_CORS_ORIGINS", `[]`, origins()),
		ok("BACKEND_CORS_ORIGINS", `null`, origins()),
		ok("BACKEND_CORS_ORIGINS", ` ["x"] `, origins("x")),
		bad("BACKEND_CORS_ORIGINS", ""),
		bad("BACKEND_CORS_ORIGINS", "https://a"),
		bad("BACKEND_CORS_ORIGINS", "https://a,https://b"),
		bad("BACKEND_CORS_ORIGINS", `["a", 1]`),
		bad("BACKEND_CORS_ORIGINS", `{"a":1}`),
		bad("BACKEND_CORS_ORIGINS", `"x"`),

		ok("LOG_LEVEL", "info", level(slog.LevelInfo)),
		ok("LOG_LEVEL", "warn", level(slog.LevelWarn)),
		ok("LOG_LEVEL", "WARNING", level(slog.LevelWarn)),
		ok("LOG_LEVEL", "error", level(slog.LevelError)),
		ok("LOG_LEVEL", "fatal", level(slog.LevelError+4)),
		ok("LOG_LEVEL", "critical", level(slog.LevelError+4)),
		ok("LOG_LEVEL", "notset", level(slog.LevelDebug)),
		bad("LOG_LEVEL", "loud"),
		bad("LOG_LEVEL", ""),
		bad("LOG_LEVEL", " info"),

		ok("KUMA_SERVER", "http://k:3001///", func(c *Config) bool { return c.KumaServer == "http://k:3001" }),
		ok("KUMA_SERVER", "https://k/base/", func(c *Config) bool { return c.KumaServer == "https://k/base" }),
		bad("KUMA_SERVER", "kuma:3001"),
		bad("KUMA_SERVER", "ftp://kuma"),
		bad("KUMA_SERVER", "http://"),
		bad("KUMA_SERVER", "http://k\x7f"),

		ok("KUMA_USERNAME", "", func(c *Config) bool { return c.KumaUsername == "" }), // as 2.0
		ok("ADMIN_PASSWORD", " a ", func(c *Config) bool { return c.AdminPassword.Value() == " a " }),
		ok("ADMIN_PASSWORD", strings.Repeat("x", 72), func(c *Config) bool { return true }),
		bad("ADMIN_PASSWORD", strings.Repeat("x", 73)),
		ok("SECRET_KEY", "  ", func(c *Config) bool { return !c.SecretKey.IsSet() }),
		ok("SECRET_KEY", "", func(c *Config) bool { return !c.SecretKey.IsSet() }),
		bad("DB_PATH", ""),

		ok("LISTEN_ADDR", ":8080", func(c *Config) bool { return c.ListenAddr == ":8080" }),
		ok("LISTEN_ADDR", "[::1]:80", func(c *Config) bool { return c.ListenAddr == "[::1]:80" }),
		bad("LISTEN_ADDR", "8080"),
		bad("LISTEN_ADDR", ":http"),
		bad("LISTEN_ADDR", ":70000"),
	}
	for _, tc := range cases {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			cfg, err := load(t, map[string]string{tc.name: tc.value})
			if !tc.valid {
				if err == nil {
					t.Fatalf("accepted, got %+v", cfg)
				}
				if len(err.Invalid) != 1 || !strings.HasPrefix(err.Invalid[0], tc.name+" ") {
					t.Fatalf("error %v, want one about %s", err, tc.name)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tc.check(cfg) {
				t.Fatalf("got %+v", cfg)
			}
		})
	}
}

func TestMissingVariablesAreListedTogether(t *testing.T) {
	_, err := load(t, map[string]string{
		"KUMA_SERVER":    "\x00",
		"KUMA_USERNAME":  "\x00",
		"KUMA_PASSWORD":  "   ",
		"ADMIN_PASSWORD": "",
		"KUMA_TIMEOUT":   "0",
		"LOG_LEVEL":      "loud",
	})
	if err == nil {
		t.Fatal("accepted")
	}
	want := []string{"KUMA_SERVER", "KUMA_USERNAME", "KUMA_PASSWORD", "ADMIN_PASSWORD"}
	if !reflect.DeepEqual(err.Missing, want) {
		t.Fatalf("missing %q, want %q", err.Missing, want)
	}
	msg := err.Error()
	for _, s := range []string{
		"missing required environment variables: KUMA_SERVER, KUMA_USERNAME, KUMA_PASSWORD, ADMIN_PASSWORD",
		"KUMA_TIMEOUT must be greater than 0",
		"LOG_LEVEL must be one of",
	} {
		if !strings.Contains(msg, s) {
			t.Errorf("%q does not contain %q", msg, s)
		}
	}
	if blank, _ := load(t, map[string]string{"KUMA_SERVER": " "}); blank != nil {
		t.Error("blank KUMA_SERVER accepted")
	}
}

func TestSecretsNeverPrint(t *testing.T) {
	cfg, err := load(t, map[string]string{
		"SECRET_KEY":     "super-secret-key",
		"KUMA_PASSWORD":  "kuma-password",
		"ADMIN_PASSWORD": "admin-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("config", "cfg", cfg, "key", cfg.SecretKey)
	text := fmt.Sprintf("%v %+v %#v %s", cfg, cfg, cfg, cfg.KumaPassword) + logged.String()
	for _, secret := range []string{"super-secret-key", "kuma-password", "admin-password"} {
		if strings.Contains(text, secret) {
			t.Fatalf("%q printed in %s", secret, text)
		}
	}
	if NewSecret("").String() != "" || NewSecret("x").String() != "[REDACTED]" {
		t.Fatal("String")
	}

	// Values of invalid secrets are not echoed either.
	_, cerr := load(t, map[string]string{"ADMIN_PASSWORD": strings.Repeat("s3cr3t", 13), "KUMA_SERVER": "ftp://user:hunter2@kuma"})
	if cerr == nil || strings.Contains(cerr.Error(), "s3cr3t") || strings.Contains(cerr.Error(), "hunter2") {
		t.Fatalf("error %v", cerr)
	}
}
