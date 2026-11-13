package httpapi

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf16"
)

// This file parses request bodies the way 2.0 does (Python's json module,
// strict mode), so that a malformed body gets the same error message and
// position, and the values echoed in validation errors keep their order.

// pyJSONError is Python's json.JSONDecodeError: a message and a position
// counted in characters (not bytes).
type pyJSONError struct {
	Msg string
	Pos int
}

func (e *pyJSONError) Error() string { return e.Msg }

// pyObject is a JSON object that keeps its keys in order (Python dicts do).
// A repeated key keeps its first position and its last value.
type pyObject struct {
	keys []string
	vals map[string]any
}

func (o *pyObject) get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// MarshalJSON writes the object with its keys in order.
func (o *pyObject) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := marshalJSON(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		vb, err := marshalJSON(o.vals[k])
		if err != nil {
			return nil, err
		}
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// pyNumber is a number as written in the request.
type pyNumber string

// MarshalJSON writes the number as received. NaN and the infinities, which
// Python accepts but JSON cannot carry, are written as null.
func (n pyNumber) MarshalJSON() ([]byte, error) {
	switch n {
	case "NaN", "Infinity", "-Infinity":
		return []byte("null"), nil
	}
	return []byte(n), nil
}

// maxJSONDepth bounds nesting. Deeper input fails with Pos -1: Python
// raises RecursionError there, which 2.0 answers with 400, not 422.
const maxJSONDepth = 500

// parsePyJSON parses s (valid UTF-8) like Python's json.loads. Values are
// nil, bool, pyNumber, string, []any or *pyObject.
func parsePyJSON(s string) (any, *pyJSONError) {
	p := &pyParser{s: []rune(s)}
	i := p.ws(0)
	v, end, err := p.value(i, 0)
	if err != nil {
		return nil, err
	}
	if end = p.ws(end); end != len(p.s) {
		return nil, &pyJSONError{"Extra data", end}
	}
	return v, nil
}

type pyParser struct{ s []rune }

func (p *pyParser) ws(i int) int {
	for i < len(p.s) && (p.s[i] == ' ' || p.s[i] == '\t' || p.s[i] == '\n' || p.s[i] == '\r') {
		i++
	}
	return i
}

func (p *pyParser) at(i int) rune {
	if i < len(p.s) {
		return p.s[i]
	}
	return -1
}

func (p *pyParser) has(i int, lit string) bool {
	r := []rune(lit)
	if i+len(r) > len(p.s) {
		return false
	}
	for k, c := range r {
		if p.s[i+k] != c {
			return false
		}
	}
	return true
}

// value is Python's scan_once: on failure at i, "Expecting value" at i.
func (p *pyParser) value(i, depth int) (any, int, *pyJSONError) {
	if depth > maxJSONDepth {
		return nil, 0, &pyJSONError{"too deeply nested", -1}
	}
	switch c := p.at(i); {
	case c == '"':
		return p.str(i + 1)
	case c == '{':
		return p.object(i+1, depth+1)
	case c == '[':
		return p.array(i+1, depth+1)
	case c == 'n' && p.has(i, "null"):
		return nil, i + 4, nil
	case c == 't' && p.has(i, "true"):
		return true, i + 4, nil
	case c == 'f' && p.has(i, "false"):
		return false, i + 5, nil
	}
	if end := p.number(i); end > i {
		return pyNumber(string(p.s[i:end])), end, nil
	}
	for _, lit := range []string{"NaN", "Infinity", "-Infinity"} {
		if p.has(i, lit) {
			return pyNumber(lit), i + len(lit), nil
		}
	}
	return nil, 0, &pyJSONError{"Expecting value", i}
}

// number matches -?(0|[1-9]\d*)(\.\d+)?([eE][-+]?\d+)? at i and returns
// its end, or i.
func (p *pyParser) number(i int) int {
	digit := func(k int) bool { c := p.at(k); return c >= '0' && c <= '9' }
	j := i
	if p.at(j) == '-' {
		j++
	}
	switch {
	case p.at(j) == '0':
		j++
	case digit(j):
		for digit(j) {
			j++
		}
	default:
		return i
	}
	if p.at(j) == '.' && digit(j+1) {
		j += 2
		for digit(j) {
			j++
		}
	}
	if c := p.at(j); c == 'e' || c == 'E' {
		k := j + 1
		if c := p.at(k); c == '+' || c == '-' {
			k++
		}
		if digit(k) {
			for digit(k) {
				k++
			}
			j = k
		}
	}
	return j
}

func (p *pyParser) object(i, depth int) (any, int, *pyJSONError) {
	obj := &pyObject{vals: map[string]any{}}
	i = p.ws(i)
	if p.at(i) == '}' {
		return obj, i + 1, nil
	}
	for {
		if p.at(i) != '"' {
			return nil, 0, &pyJSONError{"Expecting property name enclosed in double quotes", i}
		}
		k, end, err := p.str(i + 1)
		if err != nil {
			return nil, 0, err
		}
		key := k.(string)
		i = p.ws(end)
		if p.at(i) != ':' {
			return nil, 0, &pyJSONError{"Expecting ':' delimiter", i}
		}
		i = p.ws(i + 1)
		v, end, err := p.value(i, depth)
		if err != nil {
			return nil, 0, err
		}
		if _, dup := obj.vals[key]; !dup {
			obj.keys = append(obj.keys, key)
		}
		obj.vals[key] = v
		i = p.ws(end)
		if p.at(i) == '}' {
			return obj, i + 1, nil
		}
		if p.at(i) != ',' {
			return nil, 0, &pyJSONError{"Expecting ',' delimiter", i}
		}
		comma := i
		i = p.ws(i + 1)
		if p.at(i) == '}' {
			return nil, 0, &pyJSONError{"Illegal trailing comma before end of object", comma}
		}
	}
}

func (p *pyParser) array(i, depth int) (any, int, *pyJSONError) {
	arr := []any{}
	i = p.ws(i)
	if p.at(i) == ']' {
		return arr, i + 1, nil
	}
	for {
		v, end, err := p.value(i, depth)
		if err != nil {
			return nil, 0, err
		}
		arr = append(arr, v)
		i = p.ws(end)
		if p.at(i) == ']' {
			return arr, i + 1, nil
		}
		if p.at(i) != ',' {
			return nil, 0, &pyJSONError{"Expecting ',' delimiter", i}
		}
		comma := i
		i = p.ws(i + 1)
		if p.at(i) == ']' {
			return nil, 0, &pyJSONError{"Illegal trailing comma before end of array", comma}
		}
	}
}

// str is Python's scanstring (strict): begin is just after the opening
// quote.
func (p *pyParser) str(begin int) (any, int, *pyJSONError) {
	var b strings.Builder
	unterminated := &pyJSONError{"Unterminated string starting at", begin - 1}
	i := begin
	for {
		if i >= len(p.s) {
			return nil, 0, unterminated
		}
		c := p.s[i]
		switch {
		case c == '"':
			return b.String(), i + 1, nil
		case c < 0x20:
			return nil, 0, &pyJSONError{"Invalid control character at", i}
		case c != '\\':
			b.WriteRune(c)
			i++
			continue
		}
		// Escape sequence; i is the backslash.
		esc := p.at(i + 1)
		if esc < 0 {
			return nil, 0, unterminated
		}
		if esc != 'u' {
			r, ok := unescape(esc)
			if !ok {
				return nil, 0, &pyJSONError{"Invalid \\escape", i}
			}
			b.WriteRune(r)
			i += 2
			continue
		}
		u := i + 1 // the 'u'
		r, ok := p.hex4(u + 1)
		if !ok {
			return nil, 0, &pyJSONError{"Invalid \\uXXXX escape", u}
		}
		i = u + 5
		if utf16.IsSurrogate(r) && r < 0xdc00 && p.at(i) == '\\' && p.at(i+1) == 'u' {
			if r2, ok := p.hex4(i + 2); ok && r2 >= 0xdc00 && r2 <= 0xdfff {
				r = utf16.DecodeRune(r, r2)
				i += 6
			}
		}
		b.WriteRune(r) // a lone surrogate becomes U+FFFD
	}
}

func unescape(c rune) (rune, bool) {
	switch c {
	case '"', '\\', '/':
		return c, true
	case 'b':
		return '\b', true
	case 'f':
		return '\f', true
	case 'n':
		return '\n', true
	case 'r':
		return '\r', true
	case 't':
		return '\t', true
	}
	return 0, false
}

// hex4 reads four hex digits at i. Like CPython, it also requires a
// character after them.
func (p *pyParser) hex4(i int) (rune, bool) {
	if i+4 >= len(p.s) {
		return 0, false
	}
	var r rune
	for _, c := range p.s[i : i+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= c - '0'
		case c >= 'a' && c <= 'f':
			r |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			r |= c - 'A' + 10
		default:
			return 0, false
		}
	}
	return r, true
}

// marshalJSON encodes v like 2.0's responses: compact, no HTML escaping,
// no trailing newline.
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
