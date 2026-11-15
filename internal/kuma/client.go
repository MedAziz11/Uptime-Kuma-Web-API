package kuma

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound reports an object that does not exist, where Uptime Kuma
// itself would not say so clearly.
var ErrNotFound = errors.New("kuma: not found")

// Client is the typed API over a Session: one method per command the REST
// API needs, and the State the session's pushes maintain.
//
// Reads that Uptime Kuma pushes are served from State once the session is
// logged in (Sync). Other reads use CallIdempotent; writes use Call and
// are never retried. A write returns once a following read reflects it:
// for a list that Uptime Kuma pushes after the command, the write waits
// for that push (bounded by the timeout); for status pages, which it does
// not push, the client updates State itself.
//
// Acks are returned as Uptime Kuma sent them ({ok: true, ...}).
type Client struct {
	s       *Session
	st      *State
	timeout time.Duration
	log     *slog.Logger

	// Verification of suspect monitor lists (see onMonitorList).
	refreshing  atomic.Int32  // getMonitorList calls in flight
	verify      chan struct{} // a verification is due; capacity 1
	verifyDelay time.Duration // debounce before verifying

	reads flightGroup // identical concurrent reads share one call

	// Coalesced monitor list refreshes (refreshMonitors): at most one runs.
	refreshMu      sync.Mutex
	refreshRunning bool
	refreshStarted uint64        // refreshes started
	refreshDone    uint64        // the number of the last refresh finished
	refreshChanged chan struct{} // closed and replaced when one finishes
}

// Bounds of the monitor list verification.
const (
	verifyDelay      = 250 * time.Millisecond
	maxRefreshRounds = 3
)

// NewClient returns a Client on s and registers its State's handlers. Call
// it before the session's first call. Its verification goroutine stops
// with the session (Close).
func NewClient(s *Session) *Client {
	st := NewState()
	c := &Client{s: s, st: st, timeout: s.timeout, log: s.log, verify: make(chan struct{}, 1), verifyDelay: verifyDelay,
		refreshChanged: make(chan struct{})}
	st.onRacedList = c.racedList
	st.Register(s)
	s.wg.Add(1)
	go c.verifier()
	return c
}

// racedList is called on the reader goroutine for a monitor list that may
// be stale. A list our own refresh asked for is checked by that refresh.
func (c *Client) racedList() {
	if c.refreshing.Load() > 0 {
		return
	}
	select {
	case c.verify <- struct{}{}:
	default: // one is already due
	}
}

// verifier fetches the monitor list again after a suspect one, one at a
// time, a little later so that a burst of changes costs one fetch.
func (c *Client) verifier() {
	defer c.s.wg.Done()
	for {
		select {
		case <-c.s.ctx.Done():
			return
		case <-c.verify:
		}
		t := time.NewTimer(c.verifyDelay)
		select {
		case <-c.s.ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		select {
		case <-c.verify: // came during the delay: covered by this fetch
		default:
		}
		c.refreshMonitors(c.s.ctx)
	}
}

// read returns the result of a read of Uptime Kuma, sharing a call in
// flight for the same key (see flightGroup). key must identify the command
// and all its arguments.
func (c *Client) read(ctx context.Context, key string, fn func(context.Context) (json.RawMessage, error)) (json.RawMessage, error) {
	return c.reads.do(ctx, key, c.s.writes.Load(), fn)
}

// readCommand is read for a command sent with CallIdempotent.
func (c *Client) readCommand(ctx context.Context, event string, args ...any) (json.RawMessage, error) {
	key := event
	for _, a := range args {
		key += "\x00" + fmt.Sprint(a)
	}
	return c.read(ctx, key, func(ctx context.Context) (json.RawMessage, error) {
		return c.s.CallIdempotent(ctx, event, args...)
	})
}

// State returns the state built from Uptime Kuma's pushes. Its contents are
// only meaningful after Sync returned nil.
func (c *Client) State() *State { return c.st }

// Sync waits until the session is logged in, so that State holds what
// Uptime Kuma pushed at the login. It fails with ErrUnavailable while Uptime
// Kuma cannot be reached.
func (c *Client) Sync(ctx context.Context) error { return c.s.Connect(ctx) }

// write sends a command whose effect Uptime Kuma pushes as list l, and
// waits for that push.
func (c *Client) write(ctx context.Context, l List, event string, args ...any) (json.RawMessage, error) {
	v := c.st.Version(l)
	ack, err := c.s.Call(ctx, event, args...)
	if err != nil {
		return nil, err
	}
	c.awaitPush(ctx, l, v, event)
	return ack, nil
}

// awaitPush waits for list l to change after version v. The command
// succeeded either way: when the push does not come in time the answer is
// still a success, and the state catches up with the next push.
func (c *Client) awaitPush(ctx context.Context, l List, v uint64, event string) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := c.st.WaitVersion(ctx, l, v); err != nil {
		c.log.WarnContext(ctx, "Uptime Kuma did not push the change in time", "event", event, "list", l.String())
	}
}

// Monitors.

// AddMonitor sends "add" with monitor (see NewMonitor).
func (c *Client) AddMonitor(ctx context.Context, monitor map[string]any) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "add", monitor)
}

// GetMonitor sends "getMonitor": the full monitor, read from the database.
func (c *Client) GetMonitor(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.readCommand(ctx, "getMonitor", id)
}

// EditMonitor sends "editMonitor" with the full monitor (see MergeMonitor).
func (c *Client) EditMonitor(ctx context.Context, monitor map[string]any) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "editMonitor", monitor)
}

// DeleteMonitor sends "deleteMonitor".
func (c *Client) DeleteMonitor(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "deleteMonitor", id)
}

// PauseMonitor sends "pauseMonitor".
func (c *Client) PauseMonitor(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "pauseMonitor", id)
}

// ResumeMonitor sends "resumeMonitor".
func (c *Client) ResumeMonitor(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "resumeMonitor", id)
}

// MonitorBeats sends "getMonitorBeats": the heartbeats of the last hours.
func (c *Client) MonitorBeats(ctx context.Context, id int64, hours float64) (json.RawMessage, error) {
	return c.readCommand(ctx, "getMonitorBeats", id, hours)
}

// AddMonitorTag sends "addMonitorTag".
func (c *Client) AddMonitorTag(ctx context.Context, tagID, monitorID int64, value string) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "addMonitorTag", tagID, monitorID, value)
}

// DeleteMonitorTag sends "deleteMonitorTag".
func (c *Client) DeleteMonitorTag(ctx context.Context, tagID, monitorID int64, value string) (json.RawMessage, error) {
	return c.write(ctx, Monitors, "deleteMonitorTag", tagID, monitorID, value)
}

// Tags. Uptime Kuma pushes nothing after tag commands, so they are not part
// of State; the monitors carry their tags, so the monitor list is fetched
// again after a tag changes.

// Tags sends "getTags".
func (c *Client) Tags(ctx context.Context) (json.RawMessage, error) {
	return c.readCommand(ctx, "getTags")
}

// AddTag sends "addTag".
func (c *Client) AddTag(ctx context.Context, tag map[string]any) (json.RawMessage, error) {
	return c.s.Call(ctx, "addTag", tag)
}

// EditTag sends "editTag" with the full tag (id, name, color).
func (c *Client) EditTag(ctx context.Context, tag map[string]any) (json.RawMessage, error) {
	ack, err := c.s.Call(ctx, "editTag", tag)
	if err != nil {
		return nil, err
	}
	c.refreshMonitors(ctx)
	return ack, nil
}

// DeleteTag sends "deleteTag".
func (c *Client) DeleteTag(ctx context.Context, id int64) (json.RawMessage, error) {
	ack, err := c.s.Call(ctx, "deleteTag", id)
	if err != nil {
		return nil, err
	}
	c.refreshMonitors(ctx)
	return ack, nil
}

// refreshMonitors fetches the monitor list again, covering everything
// written before the call. Refreshes are coalesced: at most one runs, and a
// caller that arrives while one runs waits for the next one, which starts
// after its write, rather than for the one already running, which may have
// read the list before it. Concurrent refreshes would each see the others'
// lists as suspect and fetch again (fetchMonitors); with 200 monitors on a
// real Uptime Kuma, 12 concurrent tag writers made every refresh time out.
//
// The refresh runs on the goroutine of the caller that starts it, with a
// context that does not end with that caller's, since others wait for it;
// it is bounded by the session's timeout. Waiters honour their own ctx.
func (c *Client) refreshMonitors(ctx context.Context) {
	c.refreshMu.Lock()
	need := c.refreshStarted + 1 // the first refresh to start from now on
	for c.refreshDone < need {
		if !c.refreshRunning {
			c.refreshRunning = true
			c.refreshStarted++
			n := c.refreshStarted
			c.refreshMu.Unlock()
			c.fetchMonitors(context.WithoutCancel(ctx))
			c.refreshMu.Lock()
			c.refreshRunning = false
			c.refreshDone = n
			close(c.refreshChanged)
			c.refreshChanged = make(chan struct{})
			continue
		}
		changed := c.refreshChanged
		c.refreshMu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return
		}
		c.refreshMu.Lock()
	}
	c.refreshMu.Unlock()
}

// fetchMonitors asks for the monitor list, which Uptime Kuma pushes
// before it answers. The list is trusted only if it was the one monitor
// push between the request and the answer: an update or deletion applied
// meanwhile may be newer than the list, and another list may be a stale
// one. Then it asks again, a bounded number of times.
func (c *Client) fetchMonitors(ctx context.Context) {
	c.refreshing.Add(1)
	defer c.refreshing.Add(-1)
	for range maxRefreshRounds {
		events, lists := c.st.monitorCounts()
		if _, err := c.s.CallIdempotent(ctx, "getMonitorList"); err != nil {
			c.log.WarnContext(ctx, "cannot refresh the monitor list", "err", err)
			return
		}
		events2, lists2 := c.st.monitorCounts()
		if events2 == events && lists2 == lists+1 {
			return
		}
	}
	c.log.WarnContext(ctx, "the monitor list kept changing while it was fetched", "rounds", maxRefreshRounds)
}

// Notifications, proxies and docker hosts: Uptime Kuma pushes the whole
// list after each change.

// SaveNotification sends "addNotification": id nil adds, else edits.
func (c *Client) SaveNotification(ctx context.Context, notification map[string]any, id *int64) (json.RawMessage, error) {
	return c.write(ctx, Notifications, "addNotification", notification, id)
}

// TestNotification sends "testNotification".
func (c *Client) TestNotification(ctx context.Context, notification map[string]any) (json.RawMessage, error) {
	return c.s.Call(ctx, "testNotification", notification)
}

// DeleteNotification sends "deleteNotification".
func (c *Client) DeleteNotification(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Notifications, "deleteNotification", id)
}

// SaveProxy sends "addProxy": id nil adds, else edits.
func (c *Client) SaveProxy(ctx context.Context, proxy map[string]any, id *int64) (json.RawMessage, error) {
	return c.write(ctx, Proxies, "addProxy", proxy, id)
}

// DeleteProxy sends "deleteProxy".
func (c *Client) DeleteProxy(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Proxies, "deleteProxy", id)
}

// SaveDockerHost sends "addDockerHost": id nil adds, else edits.
func (c *Client) SaveDockerHost(ctx context.Context, host map[string]any, id *int64) (json.RawMessage, error) {
	return c.write(ctx, DockerHosts, "addDockerHost", host, id)
}

// TestDockerHost sends "testDockerHost".
func (c *Client) TestDockerHost(ctx context.Context, host map[string]any) (json.RawMessage, error) {
	return c.s.Call(ctx, "testDockerHost", host)
}

// DeleteDockerHost sends "deleteDockerHost".
func (c *Client) DeleteDockerHost(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, DockerHosts, "deleteDockerHost", id)
}

// Maintenances: Uptime Kuma pushes the list after each change, except the
// monitors of a maintenance, which are read with a command.

// AddMaintenance sends "addMaintenance".
func (c *Client) AddMaintenance(ctx context.Context, m map[string]any) (json.RawMessage, error) {
	return c.write(ctx, Maintenances, "addMaintenance", m)
}

// GetMaintenance sends "getMaintenance".
func (c *Client) GetMaintenance(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.readCommand(ctx, "getMaintenance", id)
}

// EditMaintenance sends "editMaintenance" with the full maintenance.
func (c *Client) EditMaintenance(ctx context.Context, m map[string]any) (json.RawMessage, error) {
	return c.write(ctx, Maintenances, "editMaintenance", m)
}

// DeleteMaintenance sends "deleteMaintenance".
func (c *Client) DeleteMaintenance(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Maintenances, "deleteMaintenance", id)
}

// PauseMaintenance sends "pauseMaintenance".
func (c *Client) PauseMaintenance(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Maintenances, "pauseMaintenance", id)
}

// ResumeMaintenance sends "resumeMaintenance".
func (c *Client) ResumeMaintenance(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.write(ctx, Maintenances, "resumeMaintenance", id)
}

// MaintenanceMonitors sends "getMonitorMaintenance".
func (c *Client) MaintenanceMonitors(ctx context.Context, id int64) (json.RawMessage, error) {
	return c.readCommand(ctx, "getMonitorMaintenance", id)
}

// SetMaintenanceMonitors sends "addMonitorMaintenance", which replaces the
// monitors of a maintenance.
func (c *Client) SetMaintenanceMonitors(ctx context.Context, id int64, monitors []map[string]any) (json.RawMessage, error) {
	return c.s.Call(ctx, "addMonitorMaintenance", id, monitors)
}

// Status pages. Uptime Kuma only pushes the list at login: after each
// change the client reads the page back and updates State.

// GetStatusPage sends "getStatusPage": the page's configuration.
func (c *Client) GetStatusPage(ctx context.Context, slug string) (json.RawMessage, error) {
	return c.readCommand(ctx, "getStatusPage", slug)
}

// PublicStatusPage reads /api/status-page/{slug}: the page as visitors see
// it (configuration, pinned incidents, groups of monitors, maintenances).
// An unknown slug is ErrNotFound.
func (c *Client) PublicStatusPage(ctx context.Context, slug string) (json.RawMessage, error) {
	path := "/api/status-page/" + url.PathEscape(slug)
	b, err := c.read(ctx, "GET "+path, func(ctx context.Context) (json.RawMessage, error) {
		return c.s.get(ctx, path)
	})
	if errors.Is(err, errHTTPNotFound) {
		return nil, fmt.Errorf("status page %q: %w", slug, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("%w: /api/status-page/%s: invalid JSON", ErrUnavailable, slug)
	}
	return b, nil
}

// AddStatusPage sends "addStatusPage".
func (c *Client) AddStatusPage(ctx context.Context, title, slug string) (json.RawMessage, error) {
	ack, err := c.s.Call(ctx, "addStatusPage", title, slug)
	if err != nil {
		return nil, err
	}
	var r struct {
		Slug string `json:"slug"`
	}
	if json.Unmarshal(ack, &r) == nil && r.Slug != "" {
		c.refreshStatusPage(ctx, r.Slug)
	}
	return ack, nil
}

// SaveStatusPage sends "saveStatusPage". config must hold every field of
// the page (Uptime Kuma replaces them all), its new slug included.
func (c *Client) SaveStatusPage(ctx context.Context, slug string, config map[string]any, icon string, groups []any) (json.RawMessage, error) {
	ack, err := c.s.Call(ctx, "saveStatusPage", slug, config, icon, groups)
	if err != nil {
		return nil, err
	}
	newSlug, _ := config["slug"].(string)
	if newSlug == "" {
		newSlug = slug
	}
	c.refreshStatusPage(ctx, newSlug)
	return ack, nil
}

// DeleteStatusPage sends "deleteStatusPage".
func (c *Client) DeleteStatusPage(ctx context.Context, slug string, id int64) (json.RawMessage, error) {
	ack, err := c.s.Call(ctx, "deleteStatusPage", slug)
	if err != nil {
		return nil, err
	}
	c.st.DeleteObject(StatusPages, id)
	return ack, nil
}

// refreshStatusPage reads a status page back into State.
func (c *Client) refreshStatusPage(ctx context.Context, slug string) {
	ack, err := c.GetStatusPage(ctx, slug)
	var r struct {
		Config json.RawMessage `json:"config"`
	}
	if err == nil {
		err = json.Unmarshal(ack, &r)
	}
	if err != nil || !c.st.PutObject(StatusPages, r.Config) {
		c.log.WarnContext(ctx, "cannot read a status page back", "slug", slug, "err", err)
	}
}

// PostIncident sends "postIncident".
func (c *Client) PostIncident(ctx context.Context, slug string, incident map[string]any) (json.RawMessage, error) {
	return c.s.Call(ctx, "postIncident", slug, incident)
}

// UnpinIncident sends "unpinIncident".
func (c *Client) UnpinIncident(ctx context.Context, slug string) (json.RawMessage, error) {
	return c.s.Call(ctx, "unpinIncident", slug)
}

// Database.

// DatabaseSize sends "getDatabaseSize".
func (c *Client) DatabaseSize(ctx context.Context) (json.RawMessage, error) {
	return c.readCommand(ctx, "getDatabaseSize")
}

// ShrinkDatabase sends "shrinkDatabase".
func (c *Client) ShrinkDatabase(ctx context.Context) (json.RawMessage, error) {
	return c.s.Call(ctx, "shrinkDatabase")
}
