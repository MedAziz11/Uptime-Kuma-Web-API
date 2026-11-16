// Package kumatest is an in-process fake of Uptime Kuma 2.5.11 for tests:
// the Socket.IO commands the REST API uses, the pushes that follow them and
// the public status page endpoint, on an in-memory database. Answers,
// pushes and their order follow the recordings in
// internal/kuma/testdata/2.5.11 and the server's source; where Kuma answers
// an unknown id with success or a raw JavaScript error, so does the fake.
package kumatest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma/sio/siotest"
)

// Credentials of the fake's only user.
const (
	Username = "admin"
	Password = "fake-kuma-password"
	Version  = "2.5.11"
)

// Object is a stored object, as Kuma serializes it.
type Object = map[string]any

// Server is the fake. Its exported methods are safe for concurrent use.
type Server struct {
	*siotest.Server

	mu       sync.Mutex
	sockets  map[*siotest.Socket]bool // logged in
	nextID   int
	monitors map[int]Object
	tags     map[int]Object
	// monitorTags are the monitor_tag rows.
	monitorTags   []Object
	notifications map[int]Object
	proxies       map[int]Object
	dockerHosts   map[int]Object
	maintenances  map[int]Object
	maintMonitors map[int][]int
	statusPages   map[int]Object
	apiKeys       map[int]Object
	browsers      map[int]Object
	groups        map[int][]any // status page id -> public groups
	incidents     map[int]Object
	beats         map[int][]Object
	failures      map[string]string
	noPush        bool
	calls         map[string]int
	// beforeListPush runs once in getMonitorList, between reading the list
	// and pushing it: what Kuma does meanwhile makes the list stale.
	beforeListPush func()
}

// New starts a fake. Close it with Close.
func New() *Server {
	k := &Server{
		sockets: map[*siotest.Socket]bool{}, nextID: 1,
		monitors: map[int]Object{}, tags: map[int]Object{}, notifications: map[int]Object{},
		proxies: map[int]Object{}, dockerHosts: map[int]Object{}, maintenances: map[int]Object{},
		maintMonitors: map[int][]int{}, statusPages: map[int]Object{}, apiKeys: map[int]Object{}, browsers: map[int]Object{}, groups: map[int][]any{},
		incidents: map[int]Object{}, beats: map[int][]Object{}, failures: map[string]string{},
		calls: map[string]int{},
	}
	k.Server = siotest.NewServer(siotest.Options{
		OnConnect: k.onConnect,
		OnEvent:   k.onEvent,
		HTTP:      http.HandlerFunc(k.serveHTTP),
	})
	return k
}

// Fail makes every later call of event answer {ok: false, msg}; an empty
// msg removes the failure.
func (k *Server) Fail(event, msg string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if msg == "" {
		delete(k.failures, event)
		return
	}
	k.failures[event] = msg
}

// NoPush stops (or restarts) the pushes that follow commands.
func (k *Server) NoPush(off bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.noPush = off
}

// Calls returns how many times event was handled.
func (k *Server) Calls(event string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.calls[event]
}

// Counts returns how many objects of each kind the fake holds.
func (k *Server) Counts() map[string]int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return map[string]int{
		"monitors": len(k.monitors), "tags": len(k.tags), "notifications": len(k.notifications),
		"proxies": len(k.proxies), "docker hosts": len(k.dockerHosts), "maintenances": len(k.maintenances),
		"status pages": len(k.statusPages), "api keys": len(k.apiKeys), "remote browsers": len(k.browsers),
	}
}

// Beat records a heartbeat of a monitor and pushes it with the statistics
// that follow, as Kuma does after each check.
func (k *Server) Beat(monitorID, status int, ping any) {
	k.mu.Lock()
	beat := Object{"monitorID": monitorID, "status": status, "time": "2026-11-13 14:49:43.694", "msg": "200 - OK", "ping": ping, "important": true, "retries": 0}
	k.beats[monitorID] = append(k.beats[monitorID], beat)
	k.mu.Unlock()
	k.push("heartbeat", beat)
	k.push("avgPing", monitorID, ping)
	k.push("uptime", monitorID, 24, 1)
	k.push("uptime", monitorID, 720, 1)
	k.push("uptime", monitorID, "1y", 1)
}

// Push emits an event to every logged-in socket, for tests that replay
// Kuma's pushes in the orders its awaits allow (e.g. a stale list).
func (k *Server) Push(event string, args ...any) { k.push(event, args...) }

// MonitorList returns the monitor list as Kuma would push it now.
func (k *Server) MonitorList() map[string]Object {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.objectMap(k.monitors)
}

// UpdateMonitor sets a field of a monitor and pushes it, as an edit by
// another client does.
func (k *Server) UpdateMonitor(id int, field string, value any) {
	k.mu.Lock()
	m := k.monitors[id]
	if m == nil {
		k.mu.Unlock()
		return
	}
	m[field] = value
	view := k.view(m)
	k.mu.Unlock()
	k.push("updateMonitorIntoList", Object{strconv.Itoa(id): view})
}

// OnceBeforeMonitorList makes the next getMonitorList run f after it read
// the list and before it pushes it.
func (k *Server) OnceBeforeMonitorList(f func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.beforeListPush = f
}

// CertInfo pushes a monitor's certificate info.
func (k *Server) CertInfo(monitorID int, info string) {
	k.push("certInfo", monitorID, info)
}

func (k *Server) onConnect(s *siotest.Socket) {
	k.mu.Lock()
	k.sockets[s] = false
	k.mu.Unlock()
	_ = s.Emit("info", Object{"primaryBaseURL": nil, "serverTimezone": "UTC", "serverTimezoneOffset": "+00:00"})
	_ = s.Emit("loginRequired")
}

// push emits to every logged-in socket, unless pushes are off.
func (k *Server) push(event string, args ...any) {
	k.mu.Lock()
	if k.noPush {
		k.mu.Unlock()
		return
	}
	var to []*siotest.Socket
	for s, in := range k.sockets {
		if in {
			to = append(to, s)
		}
	}
	k.mu.Unlock()
	for _, s := range to {
		_ = s.Emit(event, args...)
	}
}

func (k *Server) onEvent(s *siotest.Socket, ev siotest.Event) {
	k.mu.Lock()
	k.calls[ev.Name]++
	loggedIn := k.sockets[s]
	failure := k.failures[ev.Name]
	k.mu.Unlock()

	ack := func(v any) { _ = s.Ack(ev.ID, v) }
	switch {
	case ev.Name == "login":
		var req struct{ Username, Password string }
		_ = json.Unmarshal(arg(ev, 0), &req)
		if req.Username != Username || req.Password != Password {
			ack(Object{"ok": false, "msg": "authIncorrectCreds", "msgi18n": true})
			return
		}
		k.afterLogin(s)
		ack(Object{"ok": true, "token": "fake-token"})
		return
	case ev.Name == "loginByToken":
		if str(ev, 0) != "fake-token" {
			ack(Object{"ok": false, "msg": "authInvalidToken", "msgi18n": true})
			return
		}
		k.afterLogin(s)
		ack(Object{"ok": true})
		return
	case !loggedIn:
		ack(Object{"ok": false, "msg": "You are not logged in."})
		return
	case failure != "":
		ack(Object{"ok": false, "msg": failure})
		return
	}
	h, ok := k.handlers()[ev.Name]
	if !ok {
		return // unknown events are never acked, as by Kuma
	}
	res, after := h(ev)
	ack(res)
	if after != nil {
		after()
	}
}

func (k *Server) afterLogin(s *siotest.Socket) {
	k.mu.Lock()
	k.sockets[s] = true
	monitors, maint := k.objectMap(k.monitors), k.objectMap(k.maintenances)
	notifs, proxies, docker := values(k.notifications), values(k.proxies), values(k.dockerHosts)
	pages := k.objectMap(k.statusPages)
	keys, browsers := values(k.apiKeys), values(k.browsers)
	beats := map[int][]Object{}
	for id := range k.monitors {
		beats[id] = slices.Clone(k.beats[id])
	}
	k.mu.Unlock()
	_ = s.Emit("monitorList", monitors)
	_ = s.Emit("info", Object{"primaryBaseURL": nil, "serverTimezone": "UTC", "serverTimezoneOffset": "+00:00", "version": Version, "latestVersion": nil, "isContainer": true, "dbType": "sqlite"})
	_ = s.Emit("maintenanceList", maint)
	_ = s.Emit("notificationList", notifs)
	_ = s.Emit("proxyList", proxies)
	_ = s.Emit("dockerHostList", docker)
	_ = s.Emit("apiKeyList", keys)
	_ = s.Emit("remoteBrowserList", browsers)
	_ = s.Emit("statusPageList", pages)
	for id, b := range beats {
		key := strconv.Itoa(id) // the login pushes ids as strings
		_ = s.Emit("heartbeatList", key, b, true)
		_ = s.Emit("avgPing", key, nil)
		_ = s.Emit("uptime", key, 24, 0)
	}
}

// objectMap is a list keyed by id, as monitorList and maintenanceList.
func (k *Server) objectMap(m map[int]Object) map[string]Object {
	out := map[string]Object{}
	for id, o := range m {
		out[strconv.Itoa(id)] = k.view(o)
	}
	return out
}

// view adds the monitor's tags to a monitor.
func (k *Server) view(o Object) Object {
	c := maps.Clone(o)
	if _, isMonitor := o["accepted_statuscodes"]; isMonitor {
		tags := []any{}
		for _, mt := range k.monitorTags {
			if mt["monitor_id"] == o["id"] {
				t := maps.Clone(mt)
				if tag := k.tags[mt["tag_id"].(int)]; tag != nil {
					t["name"], t["color"] = tag["name"], tag["color"]
				}
				tags = append(tags, t)
			}
		}
		c["tags"] = tags
	}
	return c
}

func values(m map[int]Object) []Object {
	out := []Object{}
	for _, id := range slices.Sorted(maps.Keys(m)) {
		out = append(out, maps.Clone(m[id]))
	}
	return out
}

type handler func(ev siotest.Event) (ack any, after func())

func ok(extra ...any) Object {
	o := Object{"ok": true}
	for i := 0; i+1 < len(extra); i += 2 {
		o[extra[i].(string)] = extra[i+1]
	}
	return o
}

func fail(msg string) Object { return Object{"ok": false, "msg": msg} }

func (k *Server) handlers() map[string]handler {
	return map[string]handler{
		"add": k.add, "getMonitor": k.getMonitor, "editMonitor": k.editMonitor, "deleteMonitor": k.deleteMonitor,
		"pauseMonitor": k.setActive(false), "resumeMonitor": k.setActive(true), "getMonitorBeats": k.getMonitorBeats,
		"addMonitorTag": k.addMonitorTag, "deleteMonitorTag": k.deleteMonitorTag, "getMonitorList": k.getMonitorList,
		"getTags": k.getTags, "addTag": k.addTag, "editTag": k.editTag, "deleteTag": k.deleteTag,
		"addNotification":    k.save(&k.notifications, "notificationList", "notification not found", notificationRow),
		"deleteNotification": k.del(&k.notifications, "notificationList", "notification not found"),
		"testNotification":   func(siotest.Event) (any, func()) { return ok("msg", "Sent Successfully."), nil },
		"addProxy":           k.save(&k.proxies, "proxyList", "proxy not found", proxyRow),
		"deleteProxy":        k.del(&k.proxies, "proxyList", "proxy not found"),
		"addDockerHost":      k.save(&k.dockerHosts, "dockerHostList", "docker host not found", dockerRow),
		"deleteDockerHost":   k.del(&k.dockerHosts, "dockerHostList", "docker host not found"),
		"testDockerHost": func(siotest.Event) (any, func()) {
			return fail("connect ENOENT /var/run/docker.sock"), nil
		},
		"addMaintenance": k.addMaintenance, "getMaintenance": k.getMaintenance, "editMaintenance": k.editMaintenance,
		"deleteMaintenance": k.deleteMaintenance, "pauseMaintenance": k.activateMaintenance(false),
		"resumeMaintenance": k.activateMaintenance(true), "getMonitorMaintenance": k.getMonitorMaintenance,
		"addMonitorMaintenance": k.addMonitorMaintenance,
		"getStatusPage":         k.getStatusPage, "addStatusPage": k.addStatusPage, "saveStatusPage": k.saveStatusPage,
		"deleteStatusPage": k.deleteStatusPage, "postIncident": k.postIncident, "unpinIncident": k.unpinIncident,
		"addRemoteBrowser":    k.save(&k.browsers, "remoteBrowserList", "Remote browser not found", browserRow),
		"deleteRemoteBrowser": k.del(&k.browsers, "remoteBrowserList", "Remote Browser not found"),
		"testRemoteBrowser": func(ev siotest.Event) (any, func()) {
			return fail("browserType.connect: connect ECONNREFUSED " + fmt.Sprint(obj(ev, 0)["url"])), nil
		},
		"addAPIKey": k.addAPIKey, "getAPIKeyList": k.getAPIKeyList, "deleteAPIKey": k.deleteAPIKey,
		"enableAPIKey": k.activateAPIKey(true), "disableAPIKey": k.activateAPIKey(false),
		"getDatabaseSize": func(siotest.Event) (any, func()) { return ok("size", 61440), nil },
		"shrinkDatabase":  func(siotest.Event) (any, func()) { return ok(), nil },
	}
}

func arg(ev siotest.Event, i int) json.RawMessage {
	if i < len(ev.Args) {
		return ev.Args[i]
	}
	return json.RawMessage("null")
}

func str(ev siotest.Event, i int) string {
	var s string
	_ = json.Unmarshal(arg(ev, i), &s)
	return s
}

func num(ev siotest.Event, i int) int {
	var f float64
	_ = json.Unmarshal(arg(ev, i), &f)
	return int(f)
}

func obj(ev siotest.Event, i int) Object {
	var o Object
	_ = json.Unmarshal(arg(ev, i), &o)
	return o
}

func intOf(v any) int {
	f, _ := v.(float64)
	return int(f)
}

func (k *Server) newID() int {
	id := k.nextID
	k.nextID++
	return id
}

// Monitors.

func (k *Server) add(ev siotest.Event) (any, func()) {
	m := obj(ev, 0)
	if _, ok := m["conditions"].([]any); !ok {
		return fail("Cannot read properties of undefined (reading 'every')"), nil
	}
	if _, ok := m["accepted_statuscodes"].([]any); !ok {
		return fail("Cannot read properties of undefined (reading 'every')"), nil
	}
	k.mu.Lock()
	id := k.newID()
	m["id"], m["active"], m["path"], m["pathName"], m["includeSensitiveData"] = id, true, []any{m["name"]}, m["name"], true
	k.monitors[id] = m
	view := k.view(m)
	k.mu.Unlock()
	k.push("updateMonitorIntoList", Object{strconv.Itoa(id): view})
	return ok("msg", "successAdded", "msgi18n", true, "monitorID", id), nil
}

func (k *Server) getMonitor(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	m := k.monitors[num(ev, 0)]
	if m == nil {
		return fail("Cannot read properties of null (reading 'id')"), nil
	}
	return ok("monitor", k.view(m)), nil
}

func (k *Server) editMonitor(ev siotest.Event) (any, func()) {
	m := obj(ev, 0)
	id := intOf(m["id"])
	k.mu.Lock()
	cur := k.monitors[id]
	if cur == nil {
		k.mu.Unlock()
		return fail("Cannot read properties of null (reading 'user_id')"), nil
	}
	codes, _ := m["accepted_statuscodes"].([]any)
	for _, c := range codes {
		if _, ok := c.(string); !ok {
			k.mu.Unlock()
			return fail("Accepted status codes are not all strings"), nil
		}
	}
	delete(m, "tags")
	maps.Copy(cur, m)
	cur["id"] = id
	view := k.view(cur)
	k.mu.Unlock()
	k.push("updateMonitorIntoList", Object{strconv.Itoa(id): view})
	return ok("msg", "Saved.", "msgi18n", true, "monitorID", id), nil
}

func (k *Server) deleteMonitor(ev siotest.Event) (any, func()) {
	id := num(ev, 0)
	k.mu.Lock()
	delete(k.monitors, id)
	delete(k.beats, id)
	k.monitorTags = slices.DeleteFunc(k.monitorTags, func(mt Object) bool { return mt["monitor_id"] == id })
	k.mu.Unlock()
	k.push("deleteMonitorFromList", id) // even for an unknown id
	return ok("msg", "successDeleted", "msgi18n", true), nil
}

func (k *Server) setActive(active bool) handler {
	return func(ev siotest.Event) (any, func()) {
		id := num(ev, 0)
		k.mu.Lock()
		m := k.monitors[id]
		if m == nil {
			k.mu.Unlock()
			return fail("You do not own this monitor."), nil
		}
		m["active"] = active
		view := k.view(m)
		k.mu.Unlock()
		k.push("updateMonitorIntoList", Object{strconv.Itoa(id): view})
		if active {
			return ok("msg", "successResumed", "msgi18n", true), nil
		}
		return ok("msg", "successPaused", "msgi18n", true), nil
	}
}

func (k *Server) getMonitorBeats(ev siotest.Event) (any, func()) {
	if string(arg(ev, 1)) == "null" {
		return fail("Invalid period."), nil
	}
	id := num(ev, 0)
	k.mu.Lock()
	defer k.mu.Unlock()
	data := []any{}
	for i, b := range k.beats[id] {
		data = append(data, Object{"id": i + 1, "important": 1, "monitor_id": id, "status": b["status"], "msg": b["msg"], "time": b["time"], "ping": b["ping"], "duration": 0, "down_count": 0, "end_time": b["time"], "retries": 0, "response": nil})
	}
	return ok("data", data), nil
}

func (k *Server) addMonitorTag(ev siotest.Event) (any, func()) {
	tagID, monitorID, value := num(ev, 0), num(ev, 1), str(ev, 2)
	k.mu.Lock()
	if k.tags[tagID] == nil || k.monitors[monitorID] == nil {
		k.mu.Unlock()
		return fail("FOREIGN KEY constraint failed"), nil
	}
	k.monitorTags = append(k.monitorTags, Object{"id": len(k.monitorTags) + 1, "monitor_id": monitorID, "tag_id": tagID, "value": value})
	view := k.view(k.monitors[monitorID])
	k.mu.Unlock()
	k.push("updateMonitorIntoList", Object{strconv.Itoa(monitorID): view})
	return ok("msg", "successAdded", "msgi18n", true), nil
}

func (k *Server) deleteMonitorTag(ev siotest.Event) (any, func()) {
	tagID, monitorID, value := num(ev, 0), num(ev, 1), str(ev, 2)
	k.mu.Lock()
	k.monitorTags = slices.DeleteFunc(k.monitorTags, func(mt Object) bool {
		return mt["tag_id"] == tagID && mt["monitor_id"] == monitorID && mt["value"] == value
	})
	var view Object
	if m := k.monitors[monitorID]; m != nil {
		view = k.view(m)
	}
	k.mu.Unlock()
	if view != nil {
		k.push("updateMonitorIntoList", Object{strconv.Itoa(monitorID): view})
	}
	return ok("msg", "successDeleted", "msgi18n", true), nil
}

func (k *Server) getMonitorList(siotest.Event) (any, func()) {
	k.mu.Lock()
	list := k.objectMap(k.monitors)
	f := k.beforeListPush
	k.beforeListPush = nil
	k.mu.Unlock()
	if f != nil {
		f()
	}
	k.push("monitorList", list)
	return ok(), nil
}

// Tags.

func (k *Server) getTags(siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return ok("tags", values(k.tags)), nil
}

func (k *Server) addTag(ev siotest.Event) (any, func()) {
	t := obj(ev, 0)
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.newID()
	tag := Object{"id": id, "name": t["name"], "color": t["color"]}
	k.tags[id] = tag
	return ok("tag", maps.Clone(tag)), nil
}

func (k *Server) editTag(ev siotest.Event) (any, func()) {
	t := obj(ev, 0)
	k.mu.Lock()
	defer k.mu.Unlock()
	tag := k.tags[intOf(t["id"])]
	if tag == nil {
		return Object{"ok": false, "msg": "tagNotFound", "msgi18n": true}, nil
	}
	tag["name"], tag["color"] = t["name"], t["color"]
	return ok("msg", "Saved.", "msgi18n", true, "tag", maps.Clone(tag)), nil
}

func (k *Server) deleteTag(ev siotest.Event) (any, func()) {
	id := num(ev, 0)
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.tags, id)
	k.monitorTags = slices.DeleteFunc(k.monitorTags, func(mt Object) bool { return mt["tag_id"] == id })
	return ok("msg", "successDeleted", "msgi18n", true), nil
}

// Notifications, proxies, docker hosts: saved with (object, id or null),
// answered after the list was pushed.

func notificationRow(id int, in Object) Object {
	in["applyExisting"] = false
	config, _ := json.Marshal(in)
	return Object{"id": id, "name": in["name"], "active": true, "userId": 1, "isDefault": in["isDefault"] == true, "config": string(config)}
}

func proxyRow(id int, in Object) Object {
	row := Object{"id": id, "userId": 1, "protocol": in["protocol"], "host": in["host"], "port": in["port"],
		"auth": boolInt(in["auth"]), "username": in["username"], "password": in["password"],
		"active": boolInt(in["active"]), "default": boolInt(in["default"]), "createdDate": "2026-11-13 14:49:57"}
	return row
}

func dockerRow(id int, in Object) Object {
	return Object{"id": id, "userID": 1, "dockerDaemon": in["dockerDaemon"], "dockerType": in["dockerType"], "name": in["name"]}
}

func browserRow(id int, in Object) Object {
	return Object{"id": id, "url": in["url"], "name": in["name"]}
}

func boolInt(v any) int {
	if v == true {
		return 1
	}
	return 0
}

func (k *Server) save(table *map[int]Object, list, notFound string, row func(int, Object) Object) handler {
	return func(ev siotest.Event) (any, func()) {
		in := obj(ev, 0)
		k.mu.Lock()
		id := num(ev, 1)
		if string(arg(ev, 1)) == "null" {
			id = k.newID()
		} else if (*table)[id] == nil {
			k.mu.Unlock()
			return fail(notFound), nil
		}
		(*table)[id] = row(id, in)
		all := values(*table)
		k.mu.Unlock()
		k.push(list, all)
		return ok("msg", "Saved.", "msgi18n", true, "id", id), nil
	}
}

func (k *Server) del(table *map[int]Object, list, notFound string) handler {
	return func(ev siotest.Event) (any, func()) {
		id := num(ev, 0)
		k.mu.Lock()
		if (*table)[id] == nil {
			k.mu.Unlock()
			return fail(notFound), nil
		}
		delete(*table, id)
		all := values(*table)
		k.mu.Unlock()
		k.push(list, all)
		return ok("msg", "successDeleted", "msgi18n", true), nil
	}
}

// Maintenances.

func maintenanceView(id int, in Object) Object {
	m := Object{"id": id, "title": in["title"], "description": in["description"], "strategy": in["strategy"],
		"intervalDay": in["intervalDay"], "active": in["active"] != false, "dateRange": in["dateRange"],
		"timeRange": in["timeRange"], "weekdays": in["weekdays"], "daysOfMonth": in["daysOfMonth"],
		"timeslotList": []any{}, "cron": in["cron"], "durationMinutes": in["durationMinutes"],
		"timezone": in["timezoneOption"], "timezoneOption": in["timezoneOption"], "status": "under-maintenance"}
	return m
}

func (k *Server) pushMaintenances() {
	k.mu.Lock()
	list := k.objectMap(k.maintenances)
	k.mu.Unlock()
	k.push("maintenanceList", list)
}

func (k *Server) addMaintenance(ev siotest.Event) (any, func()) {
	in := obj(ev, 0)
	if _, ok := in["dateRange"].([]any); !ok {
		return fail("Cannot read properties of undefined (reading '0')"), nil
	}
	k.mu.Lock()
	id := k.newID()
	k.maintenances[id] = maintenanceView(id, in)
	k.mu.Unlock()
	k.pushMaintenances()
	return ok("msg", "successAdded", "msgi18n", true, "maintenanceID", id), nil
}

func (k *Server) getMaintenance(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	m := k.maintenances[num(ev, 0)]
	if m == nil {
		return fail("Cannot read properties of null (reading 'toJSON')"), nil
	}
	return ok("maintenance", maps.Clone(m)), nil
}

func (k *Server) editMaintenance(ev siotest.Event) (any, func()) {
	in := obj(ev, 0)
	id := intOf(in["id"])
	k.mu.Lock()
	if k.maintenances[id] == nil {
		k.mu.Unlock()
		return fail("Cannot read properties of undefined (reading 'user_id')"), nil
	}
	k.maintenances[id] = maintenanceView(id, in)
	k.mu.Unlock()
	k.pushMaintenances()
	return ok("msg", "Saved.", "msgi18n", true, "maintenanceID", id), nil
}

// Kuma answers these before it pushes the list.

func (k *Server) deleteMaintenance(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	delete(k.maintenances, num(ev, 0))
	k.mu.Unlock()
	return ok("msg", "successDeleted", "msgi18n", true), k.pushMaintenances
}

func (k *Server) activateMaintenance(active bool) handler {
	return func(ev siotest.Event) (any, func()) {
		k.mu.Lock()
		m := k.maintenances[num(ev, 0)]
		if m == nil {
			k.mu.Unlock()
			return fail("Maintenance not found"), nil
		}
		m["active"] = active
		k.mu.Unlock()
		if active {
			return ok("msg", "successResumed", "msgi18n", true), k.pushMaintenances
		}
		return ok("msg", "successPaused", "msgi18n", true), k.pushMaintenances
	}
}

func (k *Server) getMonitorMaintenance(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := []any{}
	for _, id := range k.maintMonitors[num(ev, 0)] {
		if k.monitors[id] != nil {
			out = append(out, Object{"id": id})
		}
	}
	return ok("monitors", out), nil
}

func (k *Server) addMonitorMaintenance(ev siotest.Event) (any, func()) {
	var monitors []struct{ ID int }
	_ = json.Unmarshal(arg(ev, 1), &monitors)
	k.mu.Lock()
	defer k.mu.Unlock()
	ids := []int{}
	for _, m := range monitors {
		ids = append(ids, m.ID)
	}
	k.maintMonitors[num(ev, 0)] = ids
	return ok("msg", "successAdded", "msgi18n", true), nil
}

// API keys. The list is pushed before the ack of addAPIKey and after the
// other acks, for unknown ids too.

func (k *Server) pushAPIKeys() {
	k.mu.Lock()
	list := values(k.apiKeys)
	k.mu.Unlock()
	k.push("apiKeyList", list)
}

func (k *Server) addAPIKey(ev siotest.Event) (any, func()) {
	in := obj(ev, 0)
	k.mu.Lock()
	id := k.newID()
	k.apiKeys[id] = Object{"id": id, "name": in["name"], "userID": 1, "createdDate": "2026-11-13 14:50:02",
		"active": boolInt(in["active"] == true || in["active"] == 1.0), "expires": in["expires"], "status": "active"}
	k.mu.Unlock()
	k.pushAPIKeys()
	return ok("msg", "successAdded", "msgi18n", true, "key", fmt.Sprintf("uk%d_fake-clear-key", id), "keyID", id), nil
}

func (k *Server) getAPIKeyList(siotest.Event) (any, func()) {
	k.pushAPIKeys()
	return ok(), nil
}

func (k *Server) deleteAPIKey(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	delete(k.apiKeys, num(ev, 0))
	k.mu.Unlock()
	return ok("msg", "successDeleted", "msgi18n", true), k.pushAPIKeys
}

func (k *Server) activateAPIKey(active bool) handler {
	return func(ev siotest.Event) (any, func()) {
		k.mu.Lock()
		if key := k.apiKeys[num(ev, 0)]; key != nil {
			key["active"], key["status"] = boolInt(active), map[bool]string{true: "active", false: "inactive"}[active]
		}
		k.mu.Unlock()
		if active {
			return ok("msg", "successEnabled", "msgi18n", true), k.pushAPIKeys
		}
		return ok("msg", "successDisabled", "msgi18n", true), k.pushAPIKeys
	}
}

// Status pages.

func (k *Server) pageBySlug(slug string) Object {
	for _, p := range k.statusPages {
		if p["slug"] == slug {
			return p
		}
	}
	return nil
}

func (k *Server) getStatusPage(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.pageBySlug(str(ev, 0))
	if p == nil {
		return fail("No slug?"), nil
	}
	return ok("config", maps.Clone(p)), nil
}

func checkSlug(slug string) string {
	for _, c := range slug {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return "Invalid Slug"
		}
	}
	return ""
}

func (k *Server) addStatusPage(ev siotest.Event) (any, func()) {
	title, slug := strings.TrimSpace(str(ev, 0)), strings.ToLower(strings.TrimSpace(str(ev, 1)))
	if title == "" || slug == "" {
		return fail("Please input all fields"), nil
	}
	if msg := checkSlug(slug); msg != "" {
		return fail(msg), nil
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	id := k.newID()
	k.statusPages[id] = Object{"id": id, "slug": slug, "title": title, "description": nil, "icon": "/icon.svg",
		"theme": "auto", "autoRefreshInterval": 300, "published": true, "showTags": false, "domainNameList": []any{},
		"customCSS": nil, "footerText": nil, "showPoweredBy": true, "analyticsId": nil, "analyticsScriptUrl": nil,
		"analyticsType": nil, "showCertificateExpiry": false, "showOnlyLastHeartbeat": false, "rssTitle": nil}
	return ok("msg", "successAdded", "msgi18n", true, "slug", slug), nil
}

func (k *Server) saveStatusPage(ev siotest.Event) (any, func()) {
	slug, config, icon := str(ev, 0), obj(ev, 1), str(ev, 2)
	var groups []any
	_ = json.Unmarshal(arg(ev, 3), &groups)
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.pageBySlug(slug)
	switch {
	case p == nil:
		return fail("No slug?"), nil
	case config["slug"] == nil:
		return fail("Cannot read properties of undefined (reading 'length')"), nil
	case config["analyticsType"] != nil && config["analyticsType"] != "google":
		return fail("Invalid analytics type"), nil
	}
	if msg := checkSlug(config["slug"].(string)); msg != "" {
		return fail(msg), nil
	}
	for _, key := range []string{"slug", "title", "description", "theme", "showTags", "footerText", "customCSS",
		"showPoweredBy", "showCertificateExpiry", "analyticsId", "analyticsType", "domainNameList"} {
		if v, ok := config[key]; ok {
			p[key] = v
		}
	}
	p["icon"] = icon
	for i, g := range groups {
		if gm, ok := g.(map[string]any); ok {
			gm["id"] = i + 1
		}
	}
	k.groups[p["id"].(int)] = groups
	return ok("publicGroupList", groups), nil
}

func (k *Server) deleteStatusPage(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.pageBySlug(str(ev, 0))
	if p == nil {
		return fail("Status Page is not found"), nil
	}
	id := p["id"].(int)
	delete(k.statusPages, id)
	delete(k.groups, id)
	delete(k.incidents, id)
	return ok(), nil
}

func (k *Server) postIncident(ev siotest.Event) (any, func()) {
	in := obj(ev, 1)
	k.mu.Lock()
	defer k.mu.Unlock()
	p := k.pageBySlug(str(ev, 0))
	if p == nil {
		return fail("slug is not found"), nil
	}
	id := k.newID()
	inc := Object{"id": id, "style": in["style"], "title": in["title"], "content": in["content"], "pin": true,
		"active": true, "createdDate": "2026-11-13 14:50:00", "lastUpdatedDate": nil, "status_page_id": p["id"]}
	k.incidents[p["id"].(int)] = inc
	return ok("incident", maps.Clone(inc)), nil
}

func (k *Server) unpinIncident(ev siotest.Event) (any, func()) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if p := k.pageBySlug(str(ev, 0)); p != nil {
		delete(k.incidents, p["id"].(int))
	}
	return ok(), nil
}

// serveHTTP serves GET /api/status-page/{slug}.
func (k *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	slug, found := strings.CutPrefix(r.URL.Path, "/api/status-page/")
	if !found || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	k.mu.Lock()
	p := k.pageBySlug(strings.ToLower(slug))
	var body Object
	if p != nil {
		config := maps.Clone(p)
		delete(config, "id")
		delete(config, "domainNameList")
		incidents := []any{}
		if inc := k.incidents[p["id"].(int)]; inc != nil {
			incidents = append(incidents, maps.Clone(inc))
		}
		groups := k.groups[p["id"].(int)]
		if groups == nil {
			groups = []any{}
		}
		body = Object{"config": config, "incidents": incidents, "publicGroupList": groups, "maintenanceList": []any{}}
	}
	k.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if body == nil {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"status":"fail","msg":"Status Page Not Found"}`)
		return
	}
	_ = json.NewEncoder(w).Encode(body)
}
