package kuma

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Errors returned by Session. Callers test them with errors.Is.
var (
	// ErrUnavailable reports that Uptime Kuma cannot be used: it is not
	// reachable, the connection was lost (in-flight calls fail with it), the
	// login failed, or the session is closed. HTTP 503.
	ErrUnavailable = errors.New("kuma: Uptime Kuma is unavailable")
	// ErrTimeout reports that Uptime Kuma did not answer a call within the
	// deadline. HTTP 504.
	ErrTimeout = errors.New("kuma: Uptime Kuma did not answer in time")
	// ErrLoginRejected reports that Uptime Kuma refused the configured
	// credentials (or rate limited the login). It is wrapped together with
	// ErrUnavailable.
	ErrLoginRejected = errors.New("kuma: Uptime Kuma rejected the login")
	// ErrTwoFactorRequired reports that the configured account has two-factor
	// authentication enabled, which an unattended client cannot satisfy. It
	// is a configuration error: the session stops trying to connect. It is
	// wrapped together with ErrUnavailable.
	ErrTwoFactorRequired = errors.New("kuma: the Uptime Kuma account requires two-factor authentication, which the API cannot use")
)

// CommandError is Uptime Kuma's answer {ok: false, msg} to a command.
type CommandError struct {
	// Event is the command that failed.
	Event string
	// Msg is Kuma's message, verbatim. With I18n set it is a translation
	// key (e.g. "authIncorrectCreds") rather than an English sentence.
	Msg  string
	I18n bool
}

func (e *CommandError) Error() string {
	if e.Msg == "" {
		return "kuma: " + e.Event + " failed"
	}
	return "kuma: " + e.Event + ": " + e.Msg
}

var okKey = []byte(`"ok"`)

// commandError returns a *CommandError if ack is an object whose "ok" member
// is false, and nil otherwise (including answers that are not objects or
// carry no "ok", such as needSetup's boolean).
func commandError(event string, ack json.RawMessage) error {
	if !isObject(ack) || !bytes.Contains(ack, okKey) {
		return nil
	}
	var r struct {
		OK      *bool           `json:"ok"`
		Msg     json.RawMessage `json:"msg"`
		MsgI18n bool            `json:"msgi18n"`
	}
	if json.Unmarshal(ack, &r) != nil || r.OK == nil || *r.OK {
		return nil
	}
	return &CommandError{Event: event, Msg: message(r.Msg), I18n: r.MsgI18n}
}

// message renders Kuma's msg, which is a string except in odd error paths
// where a raw value is sent.
func message(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	if string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func isObject(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}
