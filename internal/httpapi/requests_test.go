package httpapi

import (
	"strings"
	"testing"
	"time"
)

func TestUserCreateEdgeCases(t *testing.T) {
	for _, c := range []struct {
		ct, body string
		status   int
		detail   string
	}{
		{"application/json", "\xff{}", 400, `"There was an error parsing the body"`},
		{"application/json", `{"username":"x",`, 422, `[{"type":"json_invalid","loc":["body",16],"msg":"JSON decode error","input":{},"ctx":{"error":"unexpected end of JSON input"}}]`},
		{"application/json", `{"a":1}}`, 422, `[{"type":"json_invalid","loc":["body",7],"msg":"JSON decode error","input":{},"ctx":{"error":"extra data after the JSON value"}}]`},
		{"application/json", `{"a" 1}`, 422, `[{"type":"json_invalid","loc":["body",5],"msg":"JSON decode error","input":{},"ctx":{"error":"invalid character '1' after object key"}}]`},
		{"application/json", "", 422, `[{"type":"missing","loc":["body"],"msg":"Field required","input":null}]`},
		{"application/json", `[]`, 422, `[{"type":"model_attributes_type","loc":["body"],"msg":"Input should be a valid dictionary or object to extract fields from","input":[]}]`},
		{"application/json", `{"password":"long-enough"}`, 422, `[{"type":"missing","loc":["body","username"],"msg":"Field required","input":{"password":"long-enough"}}]`},
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
