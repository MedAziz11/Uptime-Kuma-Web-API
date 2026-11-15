package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
)

// This file decodes and validates the login form and the user body with
// 2.0's rules and error messages (FastAPI forms, pydantic models).

// mediaType returns the lower-cased media type of a Content-Type header.
func mediaType(contentType string) (string, map[string]string) {
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt, _, _ = strings.Cut(contentType, ";")
		return strings.ToLower(strings.TrimSpace(mt)), nil
	}
	return mt, params
}

// loginForm is OAuth2PasswordRequestForm: the fields 2.0 validates.
type loginForm struct {
	Username, Password string
}

// parseLoginForm reads a login form (urlencoded or multipart; any other
// body is an empty form, as in 2.0) and validates it.
func parseLoginForm(contentType string, body []byte) (loginForm, *apiError) {
	var fields map[string]string
	switch mt, params := mediaType(contentType); mt {
	case "application/x-www-form-urlencoded":
		fields = parseURLEncoded(body)
	case "multipart/form-data":
		var err error
		if fields, err = parseMultipart(body, params["boundary"]); err != nil {
			return loginForm{}, errBodyParsing()
		}
	}

	// Empty values count as missing, as FastAPI treats form fields.
	var issues []validationIssue
	if gt := fields["grant_type"]; gt != "" && gt != "password" {
		issues = append(issues, validationIssue{
			Type: "string_pattern_mismatch", Loc: []any{"body", "grant_type"},
			Msg: "String should match pattern '^password$'", Input: gt,
			Ctx: map[string]any{"pattern": "^password$"},
		})
	}
	for _, name := range []string{"username", "password"} {
		if fields[name] == "" {
			issues = append(issues, missing([]any{"body", name}, nil))
		}
	}
	if issues != nil {
		return loginForm{}, errValidation(issues)
	}
	return loginForm{Username: fields["username"], Password: fields["password"]}, nil
}

func missing(loc []any, input any) validationIssue {
	return validationIssue{Type: "missing", Loc: loc, Msg: "Field required", Input: input}
}

// parseURLEncoded decodes a form as Starlette does: fields split on '&',
// the last value of a repeated name wins, '+' is a space, the raw bytes
// are Latin-1 and %XX sequences are UTF-8 (invalid ones replaced, malformed
// escapes kept as they are).
func parseURLEncoded(body []byte) map[string]string {
	fields := map[string]string{}
	for _, field := range bytes.Split(body, []byte("&")) {
		if len(field) == 0 {
			continue
		}
		name, value, _ := bytes.Cut(field, []byte("="))
		fields[unquotePlus(name)] = unquotePlus(value)
	}
	return fields
}

// unquotePlus is Python's urllib.parse.unquote_plus applied to the
// Latin-1 decoding of b.
func unquotePlus(b []byte) string {
	var out strings.Builder
	var pending []byte // decoded %XX bytes, flushed as UTF-8
	flush := func() {
		for len(pending) > 0 {
			r, size := utf8.DecodeRune(pending)
			out.WriteRune(r) // RuneError for invalid bytes, as errors="replace"
			pending = pending[size:]
		}
	}
	for i := 0; i < len(b); i++ {
		c := b[i]
		if c == '%' && i+2 < len(b) && isHex(b[i+1]) && isHex(b[i+2]) {
			pending = append(pending, unhex(b[i+1])<<4|unhex(b[i+2]))
			i += 2
			continue
		}
		flush()
		if c == '+' {
			c = ' '
		}
		out.WriteRune(rune(c)) // Latin-1
	}
	flush()
	return out.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// maxFormParts bounds the parts of a multipart form.
const maxFormParts = 100

func parseMultipart(body []byte, boundary string) (map[string]string, error) {
	if boundary == "" {
		return nil, errors.New("no boundary")
	}
	fields := map[string]string{}
	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for n := 0; ; n++ {
		part, err := r.NextPart()
		if errors.Is(err, io.EOF) {
			return fields, nil
		}
		if err != nil {
			return nil, err
		}
		if n >= maxFormParts {
			return nil, errors.New("too many parts")
		}
		value, err := io.ReadAll(part)
		if err != nil {
			return nil, err
		}
		if name := part.FormName(); name != "" {
			fields[name] = strings.ToValidUTF8(string(value), "�")
		}
	}
}

// UserCreate's constraints in 2.0 (app/schemas/user.py).
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

const (
	usernameMinLength = 3
	usernameMaxLength = 20
	passwordMinLength = 8
)

// parseUserCreate decodes and validates a POST /users body like 2.0: every
// field error is reported.
func parseUserCreate(contentType string, body []byte) (username, password string, _ *apiError) {
	value, aerr := decodeJSONBody(contentType, body)
	if aerr != nil {
		return "", "", aerr
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return "", "", errValidation([]validationIssue{notAnObject(value)})
	}

	var issues []validationIssue
	username, issue := stringField(obj, "username")
	if issue == nil {
		issue = checkLength("username", username, usernameMinLength, usernameMaxLength)
	}
	if issue == nil && !usernamePattern.MatchString(username) {
		issue = &validationIssue{
			Type: "string_pattern_mismatch", Loc: []any{"body", "username"},
			Msg: "String should match pattern '^[A-Za-z0-9_.-]+$'", Input: username,
			Ctx: map[string]any{"pattern": "^[A-Za-z0-9_.-]+$"},
		}
	}
	if issue != nil {
		issues = append(issues, *issue)
	}

	password, issue = stringField(obj, "password")
	if issue == nil {
		issue = checkLength("password", password, passwordMinLength, 0)
	}
	if issue == nil && len(password) > auth.MaxPasswordBytes {
		issue = &validationIssue{
			Type: "value_error", Loc: []any{"body", "password"},
			Msg:   fmt.Sprintf("Value error, password must be at most %d bytes long", auth.MaxPasswordBytes),
			Input: password, Ctx: map[string]any{"error": struct{}{}},
		}
	}
	if issue != nil {
		issues = append(issues, *issue)
	}
	if issues != nil {
		return "", "", errValidation(issues)
	}
	return username, password, nil
}

func notAnObject(input any) validationIssue {
	return validationIssue{
		Type: "model_attributes_type", Loc: []any{"body"},
		Msg: "Input should be a valid dictionary or object to extract fields from", Input: input,
	}
}

func stringField(obj map[string]any, name string) (string, *validationIssue) {
	v, ok := obj[name]
	if !ok {
		i := missing([]any{"body", name}, obj)
		return "", &i
	}
	s, ok := v.(string)
	if !ok {
		return "", &validationIssue{Type: "string_type", Loc: []any{"body", name}, Msg: "Input should be a valid string", Input: v}
	}
	return s, nil
}

// checkLength applies pydantic's min_length/max_length (in characters);
// max 0 means no maximum.
func checkLength(name, s string, minLen, maxLen int) *validationIssue {
	n := utf8.RuneCountInString(s)
	switch {
	case n < minLen:
		return &validationIssue{
			Type: "string_too_short", Loc: []any{"body", name}, Input: s,
			Msg: fmt.Sprintf("String should have at least %d characters", minLen),
			Ctx: map[string]any{"min_length": minLen},
		}
	case maxLen > 0 && n > maxLen:
		return &validationIssue{
			Type: "string_too_long", Loc: []any{"body", name}, Input: s,
			Msg: fmt.Sprintf("String should have at most %d characters", maxLen),
			Ctx: map[string]any{"max_length": maxLen},
		}
	}
	return nil
}
