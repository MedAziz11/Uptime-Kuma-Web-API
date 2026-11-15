package kuma

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"sync"
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

// MaxHeartbeats is how many heartbeats State keeps per monitor (the length
// of the list Uptime Kuma sends at login).
const MaxHeartbeats = 100

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

	objects  [APIKeys + 1]map[int64]json.RawMessage // Monitors to APIKeys, by id
	info     json.RawMessage
	beats    map[int64][]json.RawMessage
	avgPing  map[int64]json.RawMessage
	uptime   map[int64]map[string]json.RawMessage
	certInfo map[int64]json.RawMessage
}

// NewState returns an empty State.
func NewState() *State {
	st := &State{changed: make(chan struct{})}
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
}

// Register makes s feed st: handlers for the pushed events, and a reset
// before each connection. Call it before the session's first call.
func (st *State) Register(s *Session) {
	s.OnReset(st.Reset)
	for event, h := range map[string]Handler{
		"monitorList":           st.replaceObject(Monitors),
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

// Objects returns the objects of list l (Monitors to APIKeys) by
// increasing id.
func (st *State) Objects(l List) []json.RawMessage {
	st.mu.RLock()
	defer st.mu.RUnlock()
	m := st.objects[l]
	ids := slices.Sorted(maps.Keys(m))
	out := make([]json.RawMessage, len(ids))
	for i, id := range ids {
		out[i] = m[id]
	}
	return out
}

// Object returns the object of list l with id.
func (st *State) Object(l List, id int64) (json.RawMessage, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	v, ok := st.objects[l][id]
	return v, ok
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
func (st *State) Info() (json.RawMessage, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.info, st.info != nil
}

// Heartbeats returns the last heartbeats of a monitor, oldest first.
func (st *State) Heartbeats(monitorID int64) []json.RawMessage {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return slices.Clone(st.beats[monitorID])
}

// AvgPing returns the average ping of each monitor (null when unknown).
func (st *State) AvgPing() map[int64]json.RawMessage {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return maps.Clone(st.avgPing)
}

// CertInfo returns the TLS certificate info of each monitor that has one.
func (st *State) CertInfo() map[int64]json.RawMessage {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return maps.Clone(st.certInfo)
}

// Uptime returns each monitor's uptime by period ("24", "720", "1y").
func (st *State) Uptime() map[int64]map[string]json.RawMessage {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make(map[int64]map[string]json.RawMessage, len(st.uptime))
	for id, m := range st.uptime {
		out[id] = maps.Clone(m)
	}
	return out
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

func (st *State) onUpdateMonitors(args []json.RawMessage) {
	var m map[string]json.RawMessage
	if len(args) == 0 || json.Unmarshal(args[0], &m) != nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for k, v := range m {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			st.objects[Monitors][id] = bytes.Clone(v)
		}
	}
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
	cur := st.beats[id]
	if overwrite || cur == nil {
		cur = nil
	}
	for _, b := range beats {
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
