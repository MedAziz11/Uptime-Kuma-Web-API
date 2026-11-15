package kuma

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"
)

// List names a part of the state Uptime Kuma pushes.
type List int

// The lists of State.
const (
	Monitors List = iota
	Notifications
	Proxies
	DockerHosts
	Maintenances
	StatusPages
	APIKeys
	Info
	Heartbeats
	AvgPing
	Uptime
	CertInfo
	numLists
)

var listNames = [numLists]string{
	"monitors", "notifications", "proxies", "docker hosts", "maintenances", "status pages",
	"API keys", "info", "heartbeats", "average ping", "uptime", "certificate info",
}

func (l List) String() string {
	if l < 0 || l >= numLists {
		return "list(" + strconv.Itoa(int(l)) + ")"
	}
	return listNames[l]
}

// MaxHeartbeats is how many heartbeats State keeps per monitor: the latest.
// No route reads more (GET /monitors/{id}/beats asks Uptime Kuma), and
// keeping the 100 that Uptime Kuma sends at login took 6 MB of the 29 MB
// resident with 200 monitors, over the 25 MB target (docs/performance.md).
const MaxHeartbeats = 1

// Out-of-order monitor pushes. Uptime Kuma builds a monitor list (full, or
// one monitor for updateMonitorIntoList) from database reads with many
// awaits and emits it when done, so a list read before a change committed
// can arrive after the push of that change: a deleted monitor comes back,
// a new one disappears, an edit is reverted. Monitor ids are never reused,
// so deletions are remembered (tombstones) and later pushes of those ids
// ignored; a full list that arrives close after another monitor event is
// verified by fetching it again (Client).
const (
	// raceWindow is how close to a monitor event a full list must arrive
	// to be suspect: stale lists arrive within seconds.
	raceWindow = 2 * time.Second
	// tombstoneTTL is how long a deletion is remembered once there are
	// more than maxTombstones of them.
	tombstoneTTL  = 10 * time.Minute
	maxTombstones = 1000
)

// Snapshot describes a read of State: the version of the list read, and
// whether State held a complete login state at that moment. A read with
// Synced false must not be answered as data: State was reset (the
// connection dropped) and the next login has not pushed everything yet.
type Snapshot struct {
	Version uint64
	Synced  bool
}

// State is what Uptime Kuma pushed: its objects as it sent them, keyed by
// id. Every list has a version that grows with each change, so that a
// writer can wait for its change to arrive (WaitVersion).
//
// Its handlers run on the session's reader goroutine; they decode, take the
// lock briefly and never call the session. The values handed out are never
// modified afterwards: callers may keep them but must not change them.
type State struct {
	mu       sync.RWMutex
	versions [numLists]uint64
	changed  chan struct{} // closed and replaced on every change

	// synced is true from the end of a login to the next Reset.
	synced bool

	objects  [APIKeys + 1]map[int64]json.RawMessage // Monitors to APIKeys, by id
	info     json.RawMessage
	beats    map[int64][]json.RawMessage
	avgPing  map[int64]json.RawMessage
	uptime   map[int64]map[string]json.RawMessage
	certInfo map[int64]json.RawMessage

	// Bookkeeping of out-of-order monitor pushes, for this connection.
	deleted       map[int64]time.Time // tombstones: deleted monitor ids
	monitorEvents uint64              // updates and deletions applied
	monitorLists  uint64              // full lists applied
	lastEvent     time.Time           // when the last update or deletion was applied
	onRacedList   func()              // called (unlocked) for a suspect full list
	now           func() time.Time
}

// NewState returns an empty State.
func NewState() *State {
	st := &State{changed: make(chan struct{}), now: time.Now}
	st.clearLocked()
	return st
}

func (st *State) clearLocked() {
	for i := range st.objects {
		st.objects[i] = map[int64]json.RawMessage{}
	}
	st.info = nil
	st.beats = map[int64][]json.RawMessage{}
	st.avgPing = map[int64]json.RawMessage{}
	st.uptime = map[int64]map[string]json.RawMessage{}
	st.certInfo = map[int64]json.RawMessage{}
	st.deleted = map[int64]time.Time{}
	st.monitorEvents, st.monitorLists, st.lastEvent = 0, 0, time.Time{}
	st.synced = false
}

// Register makes s feed st: handlers for the pushed events, and a reset
// before each connection. Call it before the session's first call.
func (st *State) Register(s *Session) {
	s.OnReset(st.Reset)
	s.OnLogin(st.markSynced)
	for event, h := range map[string]Handler{
		"monitorList":           st.onMonitorList,
		"maintenanceList":       st.replaceObject(Maintenances),
		"statusPageList":        st.replaceObject(StatusPages),
		"notificationList":      st.replaceArray(Notifications),
		"proxyList":             st.replaceArray(Proxies),
		"dockerHostList":        st.replaceArray(DockerHosts),
		"apiKeyList":            st.replaceArray(APIKeys),
		"updateMonitorIntoList": st.onUpdateMonitors,
		"deleteMonitorFromList": st.onDeleteMonitor,
		"info":                  st.onInfo,
		"heartbeatList":         st.onHeartbeatList,
		"heartbeat":             st.onHeartbeat,
		"avgPing":               st.onAvgPing,
		"uptime":                st.onUptime,
		"certInfo":              st.onCertInfo,
	} {
		s.Handle(event, h)
	}
}

// markSynced records that the login's pushes were all applied.
func (st *State) markSynced() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.synced = true
}

// Reset forgets everything, as before a new login.
func (st *State) Reset() {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.clearLocked()
	st.bumpLocked(allLists()...)
}

func allLists() []List {
	l := make([]List, numLists)
	for i := range l {
		l[i] = List(i)
	}
	return l
}

func (st *State) bumpLocked(lists ...List) {
	for _, l := range lists {
		st.versions[l]++
	}
	close(st.changed)
	st.changed = make(chan struct{})
}

// Version returns the version of list l.
func (st *State) Version(l List) uint64 {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.versions[l]
}

// WaitVersion waits until list l's version is above after, or ctx ends.
func (st *State) WaitVersion(ctx context.Context, l List, after uint64) error {
	for {
		st.mu.RLock()
		v, changed := st.versions[l], st.changed
		st.mu.RUnlock()
		if v > after {
			return nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (st *State) snapshotLocked(l List) Snapshot {
	return Snapshot{Version: st.versions[l], Synced: st.synced}
}

// Snapshot describes list l now without reading it: a reader that kept
// what it built from an earlier read at the same Version may reuse it, and
// must not when Synced is false. It does not allocate.
func (st *State) Snapshot(l List) Snapshot {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.snapshotLocked(l)
}

// ObjectsByID is Objects with the id of each object.
func (st *State) ObjectsByID(l List) ([]int64, []json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	m := st.objects[l]
	ids := slices.Sorted(maps.Keys(m))
	out := make([]json.RawMessage, len(ids))
	for i, id := range ids {
		out[i] = m[id]
	}
	return ids, out, st.snapshotLocked(l)
}

// Objects returns the objects of list l (Monitors to APIKeys) by
// increasing id.
func (st *State) Objects(l List) ([]json.RawMessage, Snapshot) {
	_, out, snap := st.ObjectsByID(l)
	return out, snap
}

// Object returns the object of list l with id.
func (st *State) Object(l List, id int64) (json.RawMessage, bool, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	v, ok := st.objects[l][id]
	return v, ok, st.snapshotLocked(l)
}

// PutObject stores obj under its "id" in list l: for lists Uptime Kuma does
// not push after a change (status pages).
func (st *State) PutObject(l List, obj json.RawMessage) bool {
	id, ok := objectID(obj)
	if !ok {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.objects[l][id] = bytes.Clone(obj)
	st.bumpLocked(l)
	return true
}

// DeleteObject removes the object with id from list l.
func (st *State) DeleteObject(l List, id int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.objects[l], id)
	st.bumpLocked(l)
}

// Info returns the server info, once it carries the version (Uptime Kuma
// sends a shorter one before the login).
// It is nil until then.
func (st *State) Info() (json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.info, st.snapshotLocked(Info)
}

// Heartbeats returns the last heartbeats of a monitor, oldest first.
func (st *State) Heartbeats(monitorID int64) ([]json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return slices.Clone(st.beats[monitorID]), st.snapshotLocked(Heartbeats)
}

// AvgPing returns the average ping of each monitor (null when unknown).
func (st *State) AvgPing() (map[int64]json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return maps.Clone(st.avgPing), st.snapshotLocked(AvgPing)
}

// CertInfo returns the TLS certificate info of each monitor that has one.
func (st *State) CertInfo() (map[int64]json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return maps.Clone(st.certInfo), st.snapshotLocked(CertInfo)
}

// Uptime returns each monitor's uptime by period ("24", "720", "1y").
func (st *State) Uptime() (map[int64]map[string]json.RawMessage, Snapshot) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make(map[int64]map[string]json.RawMessage, len(st.uptime))
	for id, m := range st.uptime {
		out[id] = maps.Clone(m)
	}
	return out, st.snapshotLocked(Uptime)
}

// monitorCounts returns how many monitor updates and deletions, and how
// many full monitor lists, were applied on this connection.
func (st *State) monitorCounts() (events, lists uint64) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.monitorEvents, st.monitorLists
}

// Handlers. Malformed pushes are ignored: the state then lags until the
// next push of the same list, which beats failing the connection.

// replaceObject handles a list pushed as an object keyed by id.
func (st *State) replaceObject(l List) Handler {
	return func(args []json.RawMessage) {
		var m map[string]json.RawMessage
		if len(args) == 0 || json.Unmarshal(args[0], &m) != nil {
			return
		}
		objects := make(map[int64]json.RawMessage, len(m))
		for k, v := range m {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				objects[id] = bytes.Clone(v)
			}
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.objects[l] = objects
		st.bumpLocked(l)
	}
}

// replaceArray handles a list pushed as an array of objects with an "id".
func (st *State) replaceArray(l List) Handler {
	return func(args []json.RawMessage) {
		var a []json.RawMessage
		if len(args) == 0 || json.Unmarshal(args[0], &a) != nil {
			return
		}
		objects := make(map[int64]json.RawMessage, len(a))
		for _, v := range a {
			if id, ok := objectID(v); ok {
				objects[id] = bytes.Clone(v)
			}
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		st.objects[l] = objects
		st.bumpLocked(l)
	}
}

// onMonitorList replaces the monitors, without the deleted ones, and
// reports a list that may be stale.
//
// Only a list that arrives within raceWindow after an update or a deletion
// is suspect. Verifying every list we did not ask for would never settle
// with two API instances on the same Uptime Kuma user: each one's fetch is
// an unrequested list for the other. A fetch is not a monitor event, so
// this rule does not feed itself.
func (st *State) onMonitorList(args []json.RawMessage) {
	var m map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args[0], &m) != nil {
		return
	}
	st.mu.Lock()
	objects := make(map[int64]json.RawMessage, len(m))
	for k, v := range m {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil && !st.isDeletedLocked(id) {
			objects[id] = bytes.Clone(v)
		}
	}
	st.objects[Monitors] = objects
	st.monitorLists++
	suspect := !st.lastEvent.IsZero() && st.now().Sub(st.lastEvent) < raceWindow
	f := st.onRacedList
	st.bumpLocked(Monitors)
	st.mu.Unlock()
	if suspect && f != nil {
		f()
	}
}

func (st *State) isDeletedLocked(id int64) bool {
	_, ok := st.deleted[id]
	return ok
}

// monitorEventLocked records an update or a deletion.
func (st *State) monitorEventLocked() {
	st.monitorEvents++
	st.lastEvent = st.now()
}

func (st *State) onUpdateMonitors(args []json.RawMessage) {
	var m map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args[0], &m) != nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, v := range m {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil && !st.isDeletedLocked(id) {
			st.objects[Monitors][id] = bytes.Clone(v)
		}
	}
	st.monitorEventLocked()
	st.bumpLocked(Monitors)
}

func (st *State) onDeleteMonitor(args []json.RawMessage) {
	if len(args) == 0 {
		return
	}
	id, ok := parseID(args[0])
	if !ok {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	now := st.now()
	if len(st.deleted) >= maxTombstones {
		for d, at := range st.deleted {
			if now.Sub(at) > tombstoneTTL {
				delete(st.deleted, d)
			}
		}
	}
	st.deleted[id] = now
	st.monitorEventLocked()
	delete(st.objects[Monitors], id)
	delete(st.beats, id)
	delete(st.avgPing, id)
	delete(st.uptime, id)
	delete(st.certInfo, id)
	st.bumpLocked(Monitors, Heartbeats, AvgPing, Uptime, CertInfo)
}

func (st *State) onInfo(args []json.RawMessage) {
	var info struct {
		Version json.RawMessage `json:"version"`
	}
	if len(args) == 0 || json.Unmarshal(args[0], &info) != nil || info.Version == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.info = bytes.Clone(args[0])
	st.bumpLocked(Info)
}

func (st *State) onHeartbeatList(args []json.RawMessage) {
	if len(args) < 2 {
		return
	}
	id, ok := parseID(args[0])
	var beats []json.RawMessage
	if !ok || json.Unmarshal(args[1], &beats) != nil {
		return
	}
	overwrite := len(args) > 2 && string(args[2]) == "true"
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.isDeletedLocked(id) {
		return
	}
	var cur []json.RawMessage
	if !overwrite {
		cur = st.beats[id]
	}
	for _, b := range beats[max(0, len(beats)-MaxHeartbeats):] {
		cur = append(cur, bytes.Clone(b))
	}
	st.beats[id] = tail(cur)
	st.bumpLocked(Heartbeats)
}

func (st *State) onHeartbeat(args []json.RawMessage) {
	var beat struct {
		MonitorID json.RawMessage `json:"monitorID"`
	}
	if len(args) == 0 || json.Unmarshal(args[0], &beat) != nil {
		return
	}
	id, ok := parseID(beat.MonitorID)
	if !ok {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.isDeletedLocked(id) {
		return
	}
	st.beats[id] = tail(append(st.beats[id], bytes.Clone(args[0])))
	st.bumpLocked(Heartbeats)
}

// tail keeps the last MaxHeartbeats beats, in a fresh array when it drops
// some, so that the memory of dropped beats is released.
func tail(beats []json.RawMessage) []json.RawMessage {
	if len(beats) <= MaxHeartbeats {
		return beats
	}
	return slices.Clone(beats[len(beats)-MaxHeartbeats:])
}

func (st *State) onAvgPing(args []json.RawMessage) {
	if len(args) < 2 {
		return
	}
	id, ok := parseID(args[0])
	if !ok {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.isDeletedLocked(id) {
		return
	}
	st.avgPing[id] = bytes.Clone(args[1])
	st.bumpLocked(AvgPing)
}

func (st *State) onUptime(args []json.RawMessage) {
	if len(args) < 3 {
		return
	}
	id, ok := parseID(args[0])
	if !ok {
		return
	}
	// The period is a number of hours (24, 720) or a string ("1y").
	period := string(args[1])
	var s string
	if json.Unmarshal(args[1], &s) == nil {
		period = s
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.isDeletedLocked(id) {
		return
	}
	m := st.uptime[id]
	if m == nil {
		m = map[string]json.RawMessage{}
		st.uptime[id] = m
	}
	m[period] = bytes.Clone(args[2])
	st.bumpLocked(Uptime)
}

func (st *State) onCertInfo(args []json.RawMessage) {
	if len(args) < 2 {
		return
	}
	id, ok := parseID(args[0])
	// The info is sent as a JSON document inside a string.
	var doc string
	if !ok || json.Unmarshal(args[1], &doc) != nil || !json.Valid([]byte(doc)) {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.isDeletedLocked(id) {
		return
	}
	st.certInfo[id] = json.RawMessage(doc)
	st.bumpLocked(CertInfo)
}

// parseID reads an id sent as a number or, in some pushes, as a string.
func parseID(raw json.RawMessage) (int64, bool) {
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0, false
		}
		n = json.Number(s)
	}
	id, err := n.Int64()
	return id, err == nil
}

// objectID returns the "id" member of an object.
func objectID(obj json.RawMessage) (int64, bool) {
	var o struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(obj, &o) != nil || o.ID == nil {
		return 0, false
	}
	return parseID(o.ID)
}
