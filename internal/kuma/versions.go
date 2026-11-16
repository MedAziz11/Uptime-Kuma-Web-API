package kuma

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

// minVersion is the oldest Uptime Kuma release that has a command, for the
// commands the API sends that Uptime Kuma 2.0.0 does not have (checked in
// server/server.js and server/socket-handlers/ of each release). An older
// server never answers them: the call would only end at KUMA_TIMEOUT.
var minVersion = map[string]string{
	"getIncidentHistory": "2.1.0",
	"editIncident":       "2.1.0",
	"deleteIncident":     "2.1.0",
	"resolveIncident":    "2.1.0",
}

// UnsupportedError reports that the connected Uptime Kuma is older than the
// first release with a command. HTTP 501.
type UnsupportedError struct {
	Event   string
	Have    string // the server's version
	Minimum string
}

func (e *UnsupportedError) Error() string {
	return "Uptime Kuma " + e.Have + " does not support " + e.Event +
		": it needs Uptime Kuma " + e.Minimum + " or later"
}

// supports returns an *UnsupportedError when the connected Uptime Kuma is
// older than the first release with event. It logs in first, so that the
// version (pushed in "info" at the login) is known; when it is still not
// known, the command is sent.
func (c *Client) supports(ctx context.Context, event string) error {
	minimum, ok := minVersion[event]
	if !ok {
		return nil
	}
	if err := c.Sync(ctx); err != nil {
		return err
	}
	raw, _ := c.st.Info()
	var info struct {
		Version string `json:"version"`
	}
	if raw == nil || json.Unmarshal(raw, &info) != nil || info.Version == "" {
		return nil
	}
	if olderThan(info.Version, minimum) {
		return &UnsupportedError{Event: event, Have: info.Version, Minimum: minimum}
	}
	return nil
}

// olderThan reports whether version a is older than b, comparing major,
// minor and patch numbers. A pre-release counts as its release (2.1.0-beta.1
// is not older than 2.1.0); a version that does not parse is not older.
func olderThan(a, b string) bool {
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
