package kuma

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fields2_0_0 are the fields Uptime Kuma 2.0.0's UI sends to "add" for a new
// monitor (monitorDefaults without humanReadableInterval, init(), the watch
// on monitor.type and submit() in src/pages/EditMonitor.vue of 2.0.0).
var fields2_0_0 = strings.Fields(`type name parent url method ipFamily interval retryInterval resendInterval
	maxretries notificationIDList ignoreTls upsideDown expiryNotification maxredirects accepted_statuscodes
	dns_resolve_type dns_resolve_server docker_container docker_host proxyId mqttUsername mqttPassword
	mqttTopic mqttWebsocketPath mqttSuccessMessage mqttCheckType authMethod oauth_auth_method
	httpBodyEncoding kafkaProducerBrokers kafkaProducerSaslOptions cacheBust kafkaProducerSsl
	kafkaProducerAllowAutoTopicCreation gamedigGivenPortOnly remote_browser rabbitmqNodes rabbitmqUsername
	rabbitmqPassword conditions ping_count ping_numeric packetSize ping_per_request_timeout
	port timeout snmpVersion jsonPath pushToken`)

// The defaults send only fields Uptime Kuma 2.0.0 has, whatever the type.
func TestMonitorDefaultsOnlyUse2_0_0Fields(t *testing.T) {
	for _, typ := range []string{"http", "keyword", "ping", "dns", "radius", "snmp", "ntp", "sftp", "push",
		"kafka-producer", "websocket-upgrade", "group", "real-browser"} {
		m, err := NewMonitor(map[string]any{"type": typ, "name": "x"})
		if err != nil {
			t.Fatal(err)
		}
		for k := range m {
			if !slices.Contains(fields2_0_0, k) {
				t.Errorf("%s: %s is not a field of Uptime Kuma 2.0.0's monitor form", typ, k)
			}
		}
	}
}

// Where the 2.0.0 defaults and the recorded "add" of Uptime Kuma 2.5.11's UI
// (tools/kuma-capture/lifecycle.py) have the same field, they agree.
func TestMonitorDefaultsMatchTheRecording(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "2.5.11", "lifecycle", "acks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var acks map[string]struct {
		Args []json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal(b, &acks); err != nil {
		t.Fatal(err)
	}
	var recorded map[string]any
	if err := json.Unmarshal(acks["add.http"].Args[0], &recorded); err != nil {
		t.Fatal(err)
	}
	got, err := NewMonitor(map[string]any{"name": "capture http", "url": "http://127.0.0.1:3001"})
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through JSON so that numbers compare alike.
	var norm map[string]any
	j, _ := json.Marshal(got)
	json.Unmarshal(j, &norm)
	for k, v := range recorded {
		if _, sent := norm[k]; !sent {
			continue // added after 2.0.0: the database default applies
		}
		if !reflect.DeepEqual(norm[k], v) {
			t.Errorf("%s = %v, recorded %v", k, norm[k], v)
		}
	}
	for k, want := range map[string]any{"timeout": 48.0, "ping_count": 3.0, "packetSize": 56.0, "jsonPath": "$"} {
		if norm[k] != want {
			t.Errorf("%s = %v, want %v", k, norm[k], want)
		}
	}
}

func TestNewMonitorByType(t *testing.T) {
	for _, c := range []struct {
		fields map[string]any
		want   map[string]any
	}{
		{map[string]any{"type": "ping"}, map[string]any{"timeout": 10, "httpBodyEncoding": nil}},
		{map[string]any{"type": "dns"}, map[string]any{"port": 53, "timeout": 48}},
		{map[string]any{"type": "dns", "port": 5353}, map[string]any{"port": 5353}},
		{map[string]any{"type": "radius"}, map[string]any{"port": 1812}},
		{map[string]any{"type": "snmp"}, map[string]any{"port": 161, "timeout": 5}},
		{map[string]any{"type": "ntp"}, map[string]any{"port": 123, "interval": 300}},
		{map[string]any{"type": "ntp", "interval": 60}, map[string]any{"interval": 60}},
		{map[string]any{"type": "sftp"}, map[string]any{"port": 22}},
		{map[string]any{"type": "kafka-producer"}, map[string]any{"timeout": 1}},
		{map[string]any{"type": "websocket-upgrade"}, map[string]any{"url": "wss://", "accepted_statuscodes": []any{"1000"}}},
		{map[string]any{"type": "keyword", "timeout": 5}, map[string]any{"timeout": 5, "httpBodyEncoding": "json"}},
		{map[string]any{"type": "push", "pushToken": "given"}, map[string]any{"pushToken": "given"}},
		{map[string]any{"notificationIDList": []any{1, "2"}, "accepted_statuscodes": []any{}},
			map[string]any{"notificationIDList": map[string]any{"1": true, "2": true}, "accepted_statuscodes": []any{"200-299"}}},
		{map[string]any{"notificationIDList": nil}, map[string]any{"notificationIDList": map[string]any{}}},
		{map[string]any{"notificationIDList": map[string]any{"3": true}}, map[string]any{"notificationIDList": map[string]any{"3": true}}},
	} {
		m, err := NewMonitor(c.fields)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range c.want {
			if !reflect.DeepEqual(m[k], v) {
				t.Errorf("%v: %s = %#v, want %#v", c.fields, k, m[k], v)
			}
		}
	}
	m, _ := NewMonitor(map[string]any{"type": "push"})
	if tok, _ := m["pushToken"].(string); len(tok) != pushTokenLength || strings.Trim(tok, secretChars) != "" {
		t.Fatalf("pushToken %q", tok)
	}
	m2, _ := NewMonitor(map[string]any{"type": "push"})
	if m2["pushToken"] == m["pushToken"] {
		t.Fatal("pushToken not random")
	}
}

func TestMergeMonitor(t *testing.T) {
	m, err := MergeMonitor(json.RawMessage(`{"id":1,"name":"a","interval":60,"notificationIDList":{"2":true},"accepted_statuscodes":["200-299"]}`),
		map[string]any{"name": "b", "notificationIDList": []any{json.Number("3")}})
	if err != nil {
		t.Fatal(err)
	}
	if m["name"] != "b" || m["interval"] != json.Number("60") || !reflect.DeepEqual(m["notificationIDList"], map[string]any{"3": true}) {
		t.Fatalf("%v", m)
	}
	for _, bad := range []string{`[]`, `null`, `{`} {
		if _, err := MergeMonitor(json.RawMessage(bad), nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}
