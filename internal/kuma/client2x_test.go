package kuma

import (
	"context"
	"encoding/json"
	"testing"
)

// objectField returns a field of the object with id in list l of State.
func objectField(t *testing.T, st *State, l List, id int64, name string) any {
	t.Helper()
	raw, ok, _ := st.Object(l, id)
	if !ok {
		t.Fatalf("%s %d not in State", l, id)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m[name]
}

// API key writes are visible in State when they return.
func TestClientAPIKeys(t *testing.T) {
	checkLeaks(t)
	must := mustFor(t)
	c, k, _ := newClient(t, testTimeout)
	ctx := context.Background()
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	st := c.State()
	ack := must(c.AddAPIKey(ctx, map[string]any{"name": "ci", "active": true, "expires": nil}))
	id := int64(field[float64](t, ack, "keyID"))
	if field[string](t, ack, "key") == "" {
		t.Fatalf("no clear key: %s", ack)
	}
	if objectField(t, st, APIKeys, id, "name") != "ci" || objectField(t, st, APIKeys, id, "active") != 1.0 {
		t.Fatal("added key not in State")
	}
	must(c.DisableAPIKey(ctx, id))
	if objectField(t, st, APIKeys, id, "active") != 0.0 {
		t.Fatal("disable not in State")
	}
	must(c.EnableAPIKey(ctx, id))
	if objectField(t, st, APIKeys, id, "active") != 1.0 {
		t.Fatal("enable not in State")
	}
	must(c.DeleteAPIKey(ctx, id))
	if _, ok, _ := st.Object(APIKeys, id); ok {
		t.Fatal("deleted key still in State")
	}
	k.Fail("addAPIKey", "nope")
	if _, err := c.AddAPIKey(ctx, map[string]any{"name": "x"}); err == nil {
		t.Fatal("refusal not reported")
	}
}
