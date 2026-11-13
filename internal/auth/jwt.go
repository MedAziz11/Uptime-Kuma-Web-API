package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidToken is returned for any token that must not be accepted:
// malformed, not HS256, bad signature, expired, missing or bad claims.
var ErrInvalidToken = errors.New("auth: invalid token")

// Tokens issues and verifies the access tokens of 2.0: a JWT signed with
// HS256 whose claims are exactly sub (the username), iat and exp. A token
// issued here is byte-for-byte what PyJWT issues for the same claims, and
// the verification accepts what 2.0 accepts (PyJWT with
// algorithms=["HS256"] and require=["exp", "sub"]).
type Tokens struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewTokens returns Tokens signing with key; issued tokens expire after ttl.
func NewTokens(key string, ttl time.Duration) (*Tokens, error) {
	if key == "" {
		return nil, errors.New("auth: empty signing key")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("auth: token lifetime %v must be positive", ttl)
	}
	return &Tokens{key: []byte(key), ttl: ttl, now: time.Now}, nil
}

// header is PyJWT's header for HS256, in its key order and spacing.
var header = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// Issue returns a token for subject, valid from now for the configured
// lifetime.
func (t *Tokens) Issue(subject string) string {
	now := t.now()
	return t.sign(subject, now.Unix(), now.Add(t.ttl).Unix())
}

// sign builds the token with the claims in PyJWT's order and encoding
// (json.dumps with separators=(",", ":") and ensure_ascii).
func (t *Tokens) sign(subject string, iat, exp int64) string {
	var payload []byte
	payload = append(payload, `{"sub":`...)
	payload = appendPythonJSONString(payload, subject)
	payload = append(payload, `,"iat":`...)
	payload = strconv.AppendInt(payload, iat, 10)
	payload = append(payload, `,"exp":`...)
	payload = strconv.AppendInt(payload, exp, 10)
	payload = append(payload, '}')

	signingInput := header + "." + base64.RawURLEncoding.EncodeToString(payload)
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(t.mac(signingInput))
}

func (t *Tokens) mac(signingInput string) []byte {
	m := hmac.New(sha256.New, t.key)
	m.Write([]byte(signingInput))
	return m.Sum(nil)
}

// Subject verifies token and returns its subject. Every failure wraps
// ErrInvalidToken; the wrapped reason is for logs, not for clients.
func (t *Tokens) Subject(token string) (string, error) {
	sub, err := t.verify(token)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidToken, err)
	}
	return sub, nil
}

func (t *Tokens) verify(token string) (string, error) {
	// Like PyJWT: the signature is after the last dot, the header before
	// the first; a dot inside the payload segment fails to decode below.
	signingInput, sigPart, ok := cutLast(token, ".")
	if !ok {
		return "", errors.New("not enough segments")
	}
	headerPart, payloadPart, ok := strings.Cut(signingInput, ".")
	if !ok {
		return "", errors.New("not enough segments")
	}

	headerJSON, err := decodeSegment(headerPart)
	if err != nil {
		return "", fmt.Errorf("header: %w", err)
	}
	var hdr map[string]json.RawMessage
	if err := json.Unmarshal(headerJSON, &hdr); err != nil || hdr == nil {
		return "", errors.New("header is not a JSON object")
	}
	var alg string
	if err := json.Unmarshal(hdr["alg"], &alg); err != nil || alg != "HS256" {
		return "", fmt.Errorf("algorithm %s is not allowed", hdr["alg"])
	}
	// Extensions that change how the token is read (RFC 7515 "crit",
	// RFC 7797 "b64") are not supported.
	if _, ok := hdr["crit"]; ok {
		return "", errors.New("critical header parameters are not supported")
	}
	if b64, ok := hdr["b64"]; ok && !bytes.Equal(b64, []byte("true")) {
		return "", errors.New("unencoded payloads are not supported")
	}
	if kid, ok := hdr["kid"]; ok && !isJSONString(kid) {
		return "", errors.New("key id must be a string")
	}

	sig, err := decodeSegment(sigPart)
	if err != nil {
		return "", fmt.Errorf("signature: %w", err)
	}
	if !hmac.Equal(sig, t.mac(signingInput)) {
		return "", errors.New("signature verification failed")
	}

	payloadJSON, err := decodeSegment(payloadPart)
	if err != nil {
		return "", fmt.Errorf("payload: %w", err)
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadJSON, &claims); err != nil || claims == nil {
		return "", errors.New("payload is not a JSON object")
	}
	return t.validate(claims)
}

// validate applies PyJWT's claim checks with 2.0's options (leeway 0,
// exp and sub required, no audience or issuer expected) and 2.0's own
// subject check (a non-empty string).
func (t *Tokens) validate(claims map[string]json.RawMessage) (string, error) {
	for _, name := range []string{"exp", "sub"} {
		if v, ok := claims[name]; !ok || bytes.Equal(v, []byte("null")) {
			return "", fmt.Errorf("missing %s claim", name)
		}
	}
	now := float64(t.now().UnixNano()) / 1e9
	if raw, ok := claims["iat"]; ok {
		iat, err := numericDate(raw)
		if err != nil {
			return "", fmt.Errorf("iat: %w", err)
		}
		if iat > now {
			return "", errors.New("token is not yet valid (iat)")
		}
	}
	if raw, ok := claims["nbf"]; ok {
		nbf, err := numericDate(raw)
		if err != nil {
			return "", fmt.Errorf("nbf: %w", err)
		}
		if nbf > now {
			return "", errors.New("token is not yet valid (nbf)")
		}
	}
	exp, err := numericDate(claims["exp"])
	if err != nil {
		return "", fmt.Errorf("exp: %w", err)
	}
	if exp <= now {
		return "", errors.New("token has expired")
	}
	if aud, ok := claims["aud"]; ok && isTruthy(aud) {
		return "", errors.New("unexpected audience")
	}
	if jti, ok := claims["jti"]; ok && !isJSONString(jti) {
		return "", errors.New("jti must be a string")
	}
	var sub string
	if err := json.Unmarshal(claims["sub"], &sub); err != nil {
		return "", errors.New("subject must be a string")
	}
	if sub == "" {
		return "", errors.New("empty subject")
	}
	return sub, nil
}

// numericDate reads a NumericDate claim as PyJWT does (int(value)): a
// number, or a string holding one, truncated to an integer; a number too
// large for that (int(inf) fails in Python) is rejected.
func numericDate(raw json.RawMessage) (float64, error) {
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, errors.New("must be a number")
	}
	if bytes.HasPrefix(raw, []byte(`"`)) { // int("1.5") fails in Python
		i, err := strconv.ParseInt(string(n), 10, 64)
		return float64(i), err
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, errors.New("must be a finite number")
	}
	return math.Trunc(f), nil
}

func isJSONString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`))
}

// isTruthy is Python's truth value of a JSON value.
func isTruthy(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return true
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// decodeSegment decodes unpadded base64url (as JWTs are written). It is
// stricter than PyJWT, which ignores characters outside the alphabet.
func decodeSegment(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil {
		return nil, errors.New("invalid base64url")
	}
	return b, nil
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

// appendPythonJSONString appends s encoded as Python's json.dumps does with
// ensure_ascii=True: non-ASCII characters as \uXXXX (surrogate pairs above
// U+FFFF), and outside printable ASCII only '"' and '\\' escaped.
func appendPythonJSONString(dst []byte, s string) []byte {
	const hex = "0123456789abcdef"
	u4 := func(dst []byte, r rune) []byte {
		return append(dst, '\\', 'u', hex[r>>12&0xf], hex[r>>8&0xf], hex[r>>4&0xf], hex[r&0xf])
	}
	dst = append(dst, '"')
	for _, r := range s {
		switch {
		case r == '"':
			dst = append(dst, '\\', '"')
		case r == '\\':
			dst = append(dst, '\\', '\\')
		case r == '\n':
			dst = append(dst, '\\', 'n')
		case r == '\r':
			dst = append(dst, '\\', 'r')
		case r == '\t':
			dst = append(dst, '\\', 't')
		case r == '\b':
			dst = append(dst, '\\', 'b')
		case r == '\f':
			dst = append(dst, '\\', 'f')
		case r < 0x20 || (r > 0x7e && r < 0x10000):
			dst = u4(dst, r)
		case r >= 0x10000:
			r -= 0x10000
			dst = u4(dst, 0xd800|(r>>10)&0x3ff)
			dst = u4(dst, 0xdc00|r&0x3ff)
		default:
			dst = append(dst, byte(r))
		}
	}
	return append(dst, '"')
}
