package httpapi

import (
	"fmt"
	"strings"
	"testing"
)

// errorCase is a request and the answer it must get: a status and a text
// detail, or the type and loc of the first field error (as in
// TestKumaRoutesErrors).
type errorCase struct {
	method, path, body string
	status             int
	detail             string
}

func (e *testEnv) checkErrors(t *testing.T, cases []errorCase) {
	t.Helper()
	for _, c := range cases {
		status, v := e.call(t, c.method, c.path, c.body)
		if status != c.status {
			t.Errorf("%s %s %s: %d %v, want %d", c.method, c.path, c.body, status, v, c.status)
			continue
		}
		var got string
		switch d := v.(map[string]any)["detail"].(type) {
		case string:
			got = d
		case []any:
			first := d[0].(map[string]any)
			var loc []string
			for _, l := range first["loc"].([]any) {
				loc = append(loc, fmt.Sprint(l))
			}
			got = fmt.Sprint(first["type"]) + " " + strings.Join(loc, ".")
		}
		if !strings.HasPrefix(got, c.detail) {
			t.Errorf("%s %s %s: detail %q, want %q", c.method, c.path, c.body, got, c.detail)
		}
	}
}

// checkKumaFailures makes Uptime Kuma refuse each event and checks that the
// route answers 400 with its message.
func (e *testEnv) checkKumaFailures(t *testing.T, cases []struct{ event, method, path, body string }) {
	t.Helper()
	for _, c := range cases {
		e.kuma.Fail(c.event, "Something went wrong")
		status, v := e.call(t, c.method, c.path, c.body)
		e.kuma.Fail(c.event, "")
		if status != 400 || get[string](t, v, "detail") != "Something went wrong" {
			t.Errorf("%s (%s %s): %d %v", c.event, c.method, c.path, status, v)
		}
	}
}

func TestAPIKeyRoutes(t *testing.T) {
	e := newEnv(t, Options{})
	if keys := get[[]any](t, e.ok(t, "GET", "/api_keys", ""), "api_keys"); len(keys) != 0 {
		t.Fatalf("keys %v", keys)
	}
	created := e.ok(t, "POST", "/api_keys", `{"name":"ci"}`)
	id := int(get[float64](t, created, "keyID"))
	if created["msg"] != "Added Successfully." || created["key"] != fmt.Sprintf("uk%d_fake-clear-key", id) {
		t.Fatalf("create %v", created)
	}
	path := fmt.Sprintf("/api_keys/%d", id)
	k := e.ok(t, "GET", path, "")["api_key"].(map[string]any)
	if k["name"] != "ci" || k["active"] != true || k["expires"] != nil || k["key"] != nil {
		t.Fatalf("key %v", k)
	}
	if r := e.ok(t, "POST", path+"/disable", ""); r["msg"] != "Disabled Successfully." {
		t.Fatalf("disable %v", r)
	}
	if k := get[[]any](t, e.ok(t, "GET", "/api_keys", ""), "api_keys"); len(k) != 1 || get[bool](t, k, 0, "active") || get[string](t, k, 0, "status") != "inactive" {
		t.Fatalf("after disable %v", k)
	}
	if r := e.ok(t, "POST", path+"/enable", ""); r["msg"] != "Enabled Successfully." {
		t.Fatalf("enable %v", r)
	}
	if e.ok(t, "GET", path, "")["api_key"].(map[string]any)["active"] != true {
		t.Fatal("enable not visible")
	}
	// Other fields pass through; active and expires as sent.
	other := e.ok(t, "POST", "/api_keys", `{"name":"later","active":false,"expires":"2099-01-01 00:00:00","extra":1}`)
	oid := int(get[float64](t, other, "keyID"))
	if k := e.ok(t, "GET", fmt.Sprintf("/api_keys/%d", oid), "")["api_key"].(map[string]any); k["active"] != false || k["expires"] != "2099-01-01 00:00:00" {
		t.Fatalf("second key %v", k)
	}
	if r := e.ok(t, "DELETE", path, ""); r["msg"] != "Deleted Successfully." {
		t.Fatalf("delete %v", r)
	}
	e.ok(t, "DELETE", fmt.Sprintf("/api_keys/%d", oid), "")
	if status, _ := e.call(t, "GET", path, ""); status != 404 {
		t.Fatalf("deleted key: %d", status)
	}
	if n := e.kuma.Counts()["api keys"]; n != 0 {
		t.Fatalf("%d keys left", n)
	}

	e.checkErrors(t, []errorCase{
		{"GET", "/api_keys/99999", "", 404, "API key does not exist"},
		{"POST", "/api_keys/99999/enable", "", 404, "API key does not exist"},
		{"POST", "/api_keys/99999/disable", "", 404, "API key does not exist"},
		{"DELETE", "/api_keys/99999", "", 404, "API key does not exist"},
		{"GET", "/api_keys/x", "", 422, "int_parsing path.api_key_id"},
		{"POST", "/api_keys/x/enable", "", 422, "int_parsing path.api_key_id"},
		{"POST", "/api_keys", `{}`, 422, "missing 1 required argument: 'name'"},
		{"POST", "/api_keys", `{"name":1}`, 422, "string_type body.name"},
		{"POST", "/api_keys", `{"name":"x","active":"yes"}`, 422, "bool_parsing body.active"},
		{"POST", "/api_keys", `[]`, 422, "dict_type body"},
	})

	kid := int(get[float64](t, e.ok(t, "POST", "/api_keys", `{"name":"f"}`), "keyID"))
	e.checkKumaFailures(t, []struct{ event, method, path, body string }{
		{"addAPIKey", "POST", "/api_keys", `{"name":"x"}`},
		{"enableAPIKey", "POST", fmt.Sprintf("/api_keys/%d/enable", kid), ""},
		{"disableAPIKey", "POST", fmt.Sprintf("/api_keys/%d/disable", kid), ""},
		{"deleteAPIKey", "DELETE", fmt.Sprintf("/api_keys/%d", kid), ""},
	})
}
