package kuma

import "testing"

func TestOlderThan(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"2.0.0", "2.1.0", true},
		{"2.0.2", "2.1.0", true},
		{"2.1.0", "2.1.0", false},
		{"2.1.0-beta.1", "2.1.0", false},
		{"2.0.0-beta.4", "2.1.0", true},
		{"2.5.11", "2.1.0", false},
		{"2.10.0", "2.9.0", false},
		{"1.23.17", "2.1.0", true},
		{"v2.0.0", "2.1.0", true},
		{"nightly", "2.1.0", false},
		{"2.1", "2.1.0", false},
	} {
		if got := olderThan(c.a, c.b); got != c.want {
			t.Errorf("olderThan(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
