// Package config reads the configuration from environment variables.
//
// Names, defaults and validation are those of 2.0 (app/config.py), so an
// existing deployment keeps its environment. Values are parsed the way 2.0's
// settings library parses them (for example KUMA_SSL_VERIFY accepts "yes" and
// "off", ACCESS_TOKEN_EXPIRATION accepts "1_000"). Secrets are held in Secret,
// which never prints its value.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Defaults of the optional variables.
const (
	DefaultKumaTimeout           = 10 * time.Second
	DefaultAccessTokenExpiration = 8 * 24 * time.Hour // 11520 minutes
	DefaultDBPath                = "db/db.sqlite3"
	DefaultListenAddr            = ":8000"
)

// Config is the validated configuration.
type Config struct {
	KumaServer    string // without trailing slashes
	KumaUsername  string
	KumaPassword  Secret
	KumaTimeout   time.Duration
	KumaSSLVerify bool

	AdminPassword Secret
	// SecretKey signs the access tokens. When unset, a key is generated
	// once and stored next to the database (auth.ResolveSecretKey).
	SecretKey             Secret
	AccessTokenExpiration time.Duration

	DBPath      string
	CORSOrigins []string // BACKEND_CORS_ORIGINS; empty disables CORS
	LogLevel    slog.Level
	ListenAddr  string
}

// Secret is a configuration value that must not appear in logs or errors.
// Its String, GoString and LogValue methods return a placeholder.
type Secret struct{ value string }

// NewSecret wraps a secret value.
func NewSecret(value string) Secret { return Secret{value: value} }

// Value returns the secret itself.
func (s Secret) Value() string { return s.value }

// IsSet reports whether the secret has a value.
func (s Secret) IsSet() bool { return s.value != "" }

const redacted = "[REDACTED]"

// String implements fmt.Stringer without revealing the value.
func (s Secret) String() string {
	if s.value == "" {
		return ""
	}
	return redacted
}

// GoString implements fmt.GoStringer without revealing the value.
func (s Secret) GoString() string { return strconv.Quote(s.String()) }

// LogValue implements slog.LogValuer without revealing the value.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalText implements encoding.TextMarshaler without revealing the value.
func (s Secret) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// Error lists every problem found in the environment.
type Error struct {
	Missing []string // required variables that are unset or blank
	Invalid []string // "NAME: reason" for values that cannot be used
}

func (e *Error) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing required environment variables: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Invalid) > 0 {
		parts = append(parts, "invalid environment variables: "+strings.Join(e.Invalid, "; "))
	}
	return "configuration: " + strings.Join(parts, "; ")
}

// LookupFunc returns the value of an environment variable and whether it is
// set, like os.LookupEnv.
type LookupFunc func(name string) (string, bool)

// maxBcryptPassword is bcrypt's limit; 2.0 fails at start with a longer
// ADMIN_PASSWORD.
const maxBcryptPassword = 72

// Load reads and validates the configuration. On failure the error is an
// *Error naming every variable at fault; it never contains a secret.
func Load(lookup LookupFunc) (*Config, error) {
	p := parser{lookup: lookup}
	cfg := &Config{
		KumaServer:            p.kumaServer(),
		KumaUsername:          p.required("KUMA_USERNAME"),
		KumaPassword:          p.secret("KUMA_PASSWORD", true),
		KumaTimeout:           p.seconds("KUMA_TIMEOUT", DefaultKumaTimeout),
		KumaSSLVerify:         p.boolean("KUMA_SSL_VERIFY", true),
		AdminPassword:         p.secret("ADMIN_PASSWORD", true),
		SecretKey:             p.secret("SECRET_KEY", false),
		AccessTokenExpiration: p.minutes("ACCESS_TOKEN_EXPIRATION", DefaultAccessTokenExpiration),
		DBPath:                p.dbPath(),
		CORSOrigins:           p.origins("BACKEND_CORS_ORIGINS"),
		LogLevel:              p.logLevel("LOG_LEVEL"),
		ListenAddr:            p.listenAddr("LISTEN_ADDR"),
	}
	if n := len(cfg.AdminPassword.Value()); n > maxBcryptPassword {
		p.invalid("ADMIN_PASSWORD", fmt.Sprintf("must be at most %d bytes long (bcrypt's limit), got %d bytes", maxBcryptPassword, n))
	}
	if len(p.err.Missing) > 0 || len(p.err.Invalid) > 0 {
		return nil, &p.err
	}
	return cfg, nil
}

type parser struct {
	lookup LookupFunc
	err    Error
}

func (p *parser) invalid(name, reason string) {
	p.err.Invalid = append(p.err.Invalid, name+" "+reason)
}

// required returns a variable that must be set (it may be empty, as in 2.0).
func (p *parser) required(name string) string {
	v, ok := p.lookup(name)
	if !ok {
		p.err.Missing = append(p.err.Missing, name)
	}
	return v
}

// secret returns a secret; a blank value counts as unset, as in 2.0.
func (p *parser) secret(name string, required bool) Secret {
	v, ok := p.lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		if required {
			p.err.Missing = append(p.err.Missing, name)
		}
		return Secret{}
	}
	return Secret{value: v}
}

func (p *parser) kumaServer() string {
	const name = "KUMA_SERVER"
	v, ok := p.lookup(name)
	if !ok || strings.TrimSpace(v) == "" {
		p.err.Missing = append(p.err.Missing, name)
		return ""
	}
	v = strings.TrimRight(v, "/")
	// The value is not echoed: a URL can carry credentials.
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		p.invalid(name, "must be an http:// or https:// URL with a host, e.g. http://uptime-kuma:3001")
	}
	return v
}

// stripUnderscores removes underscores that separate digits ("1_000"), as
// Python's number parsing allows; any other underscore makes s invalid.
func stripUnderscores(s string) (string, bool) {
	if !strings.Contains(s, "_") {
		return s, true
	}
	isDigit := func(b byte) bool { return b >= '0' && b <= '9' }
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '_' {
			if i == 0 || i == len(s)-1 || !isDigit(s[i-1]) || !isDigit(s[i+1]) {
				return "", false
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String(), true
}

var decimalFloat = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$|^[+-]?(inf|infinity|nan)$`)

// seconds parses a number of seconds greater than 0.
func (p *parser) seconds(name string, def time.Duration) time.Duration {
	raw, ok := p.lookup(name)
	if !ok {
		return def
	}
	s, valid := stripUnderscores(strings.TrimSpace(raw))
	if !valid || !decimalFloat.MatchString(strings.ToLower(s)) {
		p.invalid(name, fmt.Sprintf("must be a number of seconds, got %q", raw))
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	switch {
	case err != nil && !math.IsInf(f, 0):
		p.invalid(name, fmt.Sprintf("must be a number of seconds, got %q", raw))
	case math.IsNaN(f) || f <= 0:
		p.invalid(name, fmt.Sprintf("must be greater than 0, got %q", raw))
	case math.IsInf(f, 0) || f*float64(time.Second) >= math.MaxInt64:
		p.invalid(name, fmt.Sprintf("is too large, got %q", raw))
	case time.Duration(f*float64(time.Second)) <= 0:
		p.invalid(name, fmt.Sprintf("must be at least one nanosecond, got %q", raw))
	default:
		return time.Duration(f * float64(time.Second))
	}
	return def
}

var integral = regexp.MustCompile(`^[+-]?[0-9]+(\.0*)?$`)

// minutes parses a whole number of minutes greater than 0.
func (p *parser) minutes(name string, def time.Duration) time.Duration {
	raw, ok := p.lookup(name)
	if !ok {
		return def
	}
	s, valid := stripUnderscores(strings.TrimSpace(raw))
	if !valid || !integral.MatchString(s) {
		p.invalid(name, fmt.Sprintf("must be a whole number of minutes, got %q", raw))
		return def
	}
	s, _, _ = strings.Cut(s, ".")
	n, err := strconv.ParseInt(strings.TrimPrefix(s, "+"), 10, 64)
	const maxMinutes = int64(math.MaxInt64 / int64(time.Minute))
	switch {
	case err == nil && n <= 0:
		p.invalid(name, fmt.Sprintf("must be greater than 0, got %q", raw))
	case err != nil && strings.HasPrefix(s, "-"):
		p.invalid(name, fmt.Sprintf("must be greater than 0, got %q", raw))
	case err != nil || n > maxMinutes:
		p.invalid(name, fmt.Sprintf("must be at most %d minutes, got %q", maxMinutes, raw))
	default:
		return time.Duration(n) * time.Minute
	}
	return def
}

// boolean parses a boolean the way 2.0 does: 1/0, true/false, t/f, yes/no,
// y/n, on/off, in any letter case.
func (p *parser) boolean(name string, def bool) bool {
	raw, ok := p.lookup(name)
	if !ok {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	}
	p.invalid(name, fmt.Sprintf("must be a boolean (true/false, yes/no, on/off, 1/0), got %q", raw))
	return def
}

func (p *parser) dbPath() string {
	v, ok := p.lookup("DB_PATH")
	if !ok {
		return DefaultDBPath
	}
	if v == "" {
		p.invalid("DB_PATH", "must not be empty")
	}
	return v
}

// origins parses a JSON list of strings, e.g. ["https://a.example"].
func (p *parser) origins(name string) []string {
	raw, ok := p.lookup(name)
	if !ok {
		return nil
	}
	var origins []string
	if err := json.Unmarshal([]byte(raw), &origins); err != nil {
		p.invalid(name, fmt.Sprintf(`must be a JSON list of strings, e.g. ["https://dashboard.example.com"], got %q`, raw))
		return nil
	}
	return origins
}

var logLevels = map[string]slog.Level{
	"CRITICAL": slog.LevelError + 4,
	"FATAL":    slog.LevelError + 4,
	"ERROR":    slog.LevelError,
	"WARNING":  slog.LevelWarn,
	"WARN":     slog.LevelWarn,
	"INFO":     slog.LevelInfo,
	"DEBUG":    slog.LevelDebug,
	"NOTSET":   slog.LevelDebug,
}

func (p *parser) logLevel(name string) slog.Level {
	raw, ok := p.lookup(name)
	if !ok {
		return slog.LevelInfo
	}
	level, known := logLevels[strings.ToUpper(raw)]
	if !known {
		p.invalid(name, fmt.Sprintf("must be one of DEBUG, INFO, WARNING, ERROR, CRITICAL, got %q", raw))
		return slog.LevelInfo
	}
	return level
}

func (p *parser) listenAddr(name string) string {
	raw, ok := p.lookup(name)
	if !ok {
		return DefaultListenAddr
	}
	_, port, err := net.SplitHostPort(raw)
	if err == nil {
		_, err = strconv.ParseUint(port, 10, 16)
	}
	if err != nil {
		p.invalid(name, fmt.Sprintf("must be host:port or :port, e.g. :8000, got %q", raw))
	}
	return raw
}
