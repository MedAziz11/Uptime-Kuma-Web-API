package httpapi

import (
	"strings"
	"testing"
	"time"
)

// Messages and positions from CPython 3.13's json module (2.0's runtime).
func TestPyJSONErrorsMatchPython(t *testing.T) {
	for in, want := range map[string]*pyJSONError{
		`{"a":1,}`:                   {"Illegal trailing comma before end of object", 6},
		`[1,]`:                       {"Illegal trailing comma before end of array", 2},
		`{"a" 1}`:                    {"Expecting ':' delimiter", 5},
		`{"a":}`:                     {"Expecting value", 5},
		`[1 2]`:                      {"Expecting ',' delimiter", 3},
		`"abc`:                       {"Unterminated string starting at", 0},
		"\"a\x01\"":                  {"Invalid control character at", 2},
		`"\x"`:                       {"Invalid \\escape", 1},
		`"\u12"`:                     {"Invalid \\uXXXX escape", 2},
		`"\u12zz"`:                   {"Invalid \\uXXXX escape", 2},
		`1 2`:                        {"Extra data", 2},
		``:                           {"Expecting value", 0},
		`  `:                         {"Expecting value", 2},
		`tru`:                        {"Expecting value", 0},
		`01`:                         {"Extra data", 1},
		`{"a":1`:                     {"Expecting ',' delimiter", 6},
		`[`:                          {"Expecting value", 1},
		`{`:                          {"Expecting property name enclosed in double quotes", 1},
		`-`:                          {"Expecting value", 0},
		`1.`:                         {"Extra data", 1},
		`1e`:                         {"Extra data", 1},
		`{1:2}`:                      {"Expecting property name enclosed in double quotes", 1},
		`{"a":1}}`:                   {"Extra data", 7},
		`[]]`:                        {"Extra data", 2},
		` {"a" : [1, {"b": tru}]}`:   {"Expecting value", 18},
		`{"é":x}`:                    {"Expecting value", 5},
		`{,}`:                        {"Expecting property name enclosed in double quotes", 1},
		`[,]`:                        {"Expecting value", 1},
		`{"a":1 "b":2}`:              {"Expecting ',' delimiter", 7},
		`"abc\"`:                     {"Unterminated string starting at", 0},
		`-x`:                         {"Expecting value", 0},
		`1.5e`:                       {"Extra data", 3},
		"\"\t\"":                     {"Invalid control character at", 1},
		`{"a":1,"b"}`:                {"Expecting ':' delimiter", 10},
		`{"a":1, }`:                  {"Illegal trailing comma before end of object", 6},
		`{"a":1, "b":2,}`:            {"Illegal trailing comma before end of object", 13},
		`"\`:                         {"Unterminated string starting at", 0},
		`{"a":1,"b":[1,{"c":"d"},]}`: {"Illegal trailing comma before end of array", 23},
	} {
		_, err := parsePyJSON(in)
		if err == nil || *err != *want {
			t.Errorf("%q: %+v, Python %+v", in, err, want)
		}
	}
}

func TestPyJSONValues(t *testing.T) {
	for in, want := range map[string]string{
		`{"b":1,"a":[true,false,null,"x"],"b":2}`: `{"b":2,"a":[true,false,null,"x"]}`,
		`NaN`:                                `null`,
		`[-Infinity, Infinity, 1.50e+3, -0]`: `[null,null,1.50e+3,-0]`,
		`"é😀\ud800 \/\b\f\n\r\t"`:            `"é😀� /\b\f\n\r\t"`,
		` {} `:                               `{}`,
		`[]`:                                 `[]`,
		`"<&>"`:                              `"<&>"`,
	} {
		v, err := parsePyJSON(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		got, merr := marshalJSON(v)
		if merr != nil || string(got) != want {
			t.Errorf("%q: %s %v, want %s", in, got, merr, want)
		}
	}
	if _, err := parsePyJSON(strings.Repeat("[", 600) + strings.Repeat("]", 600)); err == nil || err.Pos != -1 {
		t.Fatalf("deep nesting: %v", err)
	}
	if (&pyJSONError{"m", 1}).Error() != "m" {
		t.Fatal("Error")
	}
}

func TestUserCreateEdgeCases(t *testing.T) {
	for _, c := range []struct {
		ct, body string
		status   int
		detail   string
	}{
		{"application/json", "\xff{}", 400, `"There was an error parsing the body"`},
		{"application/json", strings.Repeat("[", 600), 400, `"There was an error parsing the body"`},
		{"application/json", "\xef\xbb\xbf{\"username\":\"bomuser\",\"password\":\"long-enough\"}", 0, ""},
		{"application/vnd.api+json", `{"username":"plusjson","password":"long-enough"}`, 0, ""},
		{"APPLICATION/JSON; charset=utf-8", `{"username":"upper","password":"long-enough"}`, 0, ""},
		{"application/json;;", `{"username":"badparams","password":"long-enough"}`, 0, ""},
		{"text/plain", "\xffraw", 422, `[{"type":"model_attributes_type","loc":["body"],"msg":"Input should be a valid dictionary or object to extract fields from","input":"�raw"}]`},
		{"application/json", `{"username":["x"],"password":{"a":1}}`, 422,
			`[{"type":"string_type","loc":["body","username"],"msg":"Input should be a valid string","input":["x"]},{"type":"string_type","loc":["body","password"],"msg":"Input should be a valid string","input":{"a":1}}]`},
		{"application/json", `{"password":"short","username":"x"}`, 422,
			`[{"type":"string_too_short","loc":["body","username"],"msg":"String should have at least 3 characters","input":"x","ctx":{"min_length":3}},{"type":"string_too_short","loc":["body","password"],"msg":"String should have at least 8 characters","input":"short","ctx":{"min_length":8}}]`},
		{"application/json", `{"username":"abc\n","password":"long-enough"}`, 422,
			`[{"type":"string_pattern_mismatch","loc":["body","username"],"msg":"String should match pattern '^[A-Za-z0-9_.-]+$'","input":"abc\n","ctx":{"pattern":"^[A-Za-z0-9_.-]+$"}}]`},
	} {
		_, _, aerr := parseUserCreate(c.ct, []byte(c.body))
		if c.status == 0 {
			if aerr != nil {
				t.Errorf("%q %q: %v", c.ct, c.body, aerr)
			}
			continue
		}
		if aerr == nil {
			t.Errorf("%q %q: accepted", c.ct, c.body)
			continue
		}
		got, _ := marshalJSON(aerr.Detail)
		if aerr.status != c.status || string(got) != c.detail {
			t.Errorf("%q %q: %d %s, want %d %s", c.ct, c.body, aerr.status, got, c.status, c.detail)
		}
	}
}

func TestLoginFormDecoding(t *testing.T) {
	for _, c := range []struct {
		ct, body       string
		user, password string
		status         int
	}{
		// Starlette: last value wins, '+' is a space, %XX is UTF-8,
		// raw bytes are Latin-1, malformed escapes stay.
		{"application/x-www-form-urlencoded", "username=a&username=admin&password=p+w%2B%C3%A9", "admin", "p w+é", 0},
		{"application/x-www-form-urlencoded", "username=admin&password=\xc3\xa9", "admin", "Ã©", 0},
		{"application/x-www-form-urlencoded", "username=admin&password=%zz%4", "admin", "%zz%4", 0},
		{"application/x-www-form-urlencoded", "username=admin&password=%ff", "admin", "�", 0},
		{"application/x-www-form-urlencoded", "&&username=admin&password=x&flag", "admin", "x", 0},
		{"multipart/form-data", "--b\r\nContent-Disposition: form-data; name=\"username\"\r\n\r\nadmin\r\n--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\npw\r\n--b--\r\n", "", "", 400},
		{"multipart/form-data; boundary=b", "--b\r\nContent-Disposition: form-data; name=\"username\"\r\n\r\nadmin\r\n--b\r\nContent-Disposition: form-data; name=\"password\"\r\n\r\npw\r\n--b--\r\n", "admin", "pw", 0},
		{"multipart/form-data; boundary=b", "garbage", "", "", 422},
		{"multipart/form-data; boundary=b", strings.Repeat("--b\r\nContent-Disposition: form-data; name=\"x\"\r\n\r\nv\r\n", 101) + "--b--\r\n", "", "", 400},
		{"text/plain", "username=admin&password=x", "", "", 422},
	} {
		form, aerr := parseLoginForm(c.ct, []byte(c.body))
		switch {
		case c.status != 0 && (aerr == nil || aerr.status != c.status):
			t.Errorf("%q: %v, want %d", c.body, aerr, c.status)
		case c.status == 0 && (aerr != nil || form.Username != c.user || form.Password != c.password):
			t.Errorf("%q: %+v %v, want %q %q", c.body, form, aerr, c.user, c.password)
		}
	}
}

func TestTimestampFormat(t *testing.T) {
	for _, c := range []struct {
		ts   string
		want string
	}{
		{"2026-11-13T15:43:41.808921Z", `"2026-11-13T15:43:41.808921Z"`},
		{"2026-11-13T15:43:41Z", `"2026-11-13T15:43:41Z"`},
		{"2026-11-13T17:43:41.000001+02:00", `"2026-11-13T15:43:41.000001Z"`},
	} {
		tm, err := time.Parse(time.RFC3339Nano, c.ts)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := Timestamp(tm).MarshalJSON()
		if string(got) != c.want {
			t.Errorf("%s: %s, want %s", c.ts, got, c.want)
		}
	}
}
