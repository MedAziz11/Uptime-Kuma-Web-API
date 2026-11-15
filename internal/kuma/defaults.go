package kuma

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"maps"
)

// monitorDefaults is the monitor Uptime Kuma 2.x's own UI sends to "add"
// when the user changes nothing: monitorDefaults and init() in
// src/pages/EditMonitor.vue of 2.5.11 (MONITOR_DEFAULTS in
// tools/kuma-capture/lifecycle.py). "add" fails with raw JavaScript errors
// on a partial object.
func monitorDefaults() map[string]any {
	return map[string]any{
		"type": "http", "name": "", "parent": nil, "url": "https://", "wsSubprotocol": "", "method": "GET",
		"protocol": nil, "location": "world", "ipFamily": nil, "interval": 60, "retryInterval": 60,
		"resendInterval": 0, "maxretries": 0, "retryOnlyOnStatusCodeFailure": false, "notificationIDList": map[string]any{},
		"ignoreTls": false, "upsideDown": false, "expiryNotification": false, "domainExpiryNotification": true,
		"maxredirects": 10, "accepted_statuscodes": []any{"200-299"}, "saveResponse": false, "saveErrorResponse": true,
		"responseMaxLength": 1024, "dns_resolve_type": "A", "dns_resolve_server": "1.1.1.1", "docker_container": "",
		"docker_host": nil, "proxyId": nil, "basic_auth_user": "", "basic_auth_pass": "", "bearer_token": "",
		"mqttUsername": "", "mqttPassword": "", "mqttTopic": "", "mqttWebsocketPath": "", "mqttSuccessMessage": "",
		"mqttCheckType": "keyword", "authMethod": nil, "oauth_auth_method": "client_secret_basic",
		"httpBodyEncoding": "json", "kafkaProducerBrokers": []any{}, "kafkaProducerSaslOptions": map[string]any{"mechanism": "None"},
		"cacheBust": false, "kafkaProducerSsl": false, "kafkaProducerAllowAutoTopicCreation": false,
		"gamedigGivenPortOnly": true, "gamedigToken": "", "remote_browser": nil, "screenshot_delay": 0,
		"rabbitmqNodes": []any{}, "rabbitmqUsername": "", "rabbitmqPassword": "", "conditions": []any{},
		"system_service_name": "", "sshAuthMethod": "password", "ntpStratumThreshold": 5,
		"ntpTimeOffsetThreshold": 1000, "ntpRootDispersionThreshold": 500,
		// init() for a new monitor
		"ping_count": 3, "ping_numeric": true, "packetSize": 56, "ping_per_request_timeout": 2,
		// watch on monitor.type
		"snmpVersion": "2c", "jsonPath": "$",
	}
}

// typeDefaults returns the defaults the UI derives from the monitor type
// (the watch on monitor.type and submit() in EditMonitor.vue).
func typeDefaults(typ string) map[string]any {
	d := map[string]any{"timeout": 48}
	switch typ {
	case "ping":
		d["timeout"] = 10
	case "kafka-producer":
		d["timeout"] = 1
	case "snmp":
		d["timeout"], d["port"] = 5, 161
	case "dns":
		d["port"] = 53
	case "radius":
		d["port"] = 1812
	case "ntp":
		d["port"], d["interval"] = 123, 300
	case "sftp":
		d["port"] = 22
	case "websocket-upgrade":
		d["url"], d["accepted_statuscodes"] = "wss://", []any{"1000"}
	}
	switch typ {
	case "http", "keyword", "json-query":
	default:
		d["httpBodyEncoding"] = nil // only those types send a body
	}
	return d
}

// pushTokenLength is the UI's pushTokenLength.
const pushTokenLength = 32

// NewMonitor returns the object to send to "add": the UI's defaults, the
// fields of the request, and the defaults that depend on the type, in the
// format Kuma expects (ConvertMonitorInput).
func NewMonitor(fields map[string]any) (map[string]any, error) {
	m := monitorDefaults()
	maps.Copy(m, fields)
	typ, _ := m["type"].(string)
	for k, v := range typeDefaults(typ) {
		if _, ok := fields[k]; !ok {
			m[k] = v
		}
	}
	if tok, _ := m["pushToken"].(string); typ == "push" && tok == "" {
		tok, err := genSecret(pushTokenLength)
		if err != nil {
			return nil, err
		}
		m["pushToken"] = tok
	}
	ConvertMonitorInput(m)
	return m, nil
}

// MergeMonitor returns the object to send to "editMonitor": the current
// monitor (as getMonitor or the monitor list returns it) with the changes
// applied.
func MergeMonitor(current json.RawMessage, changes map[string]any) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(current))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, fmt.Errorf("kuma: unexpected monitor %.80s: %v", current, err)
	}
	maps.Copy(m, changes)
	ConvertMonitorInput(m)
	return m, nil
}

// ConvertMonitorInput applies 2.0's conversions (_convert_monitor_input of
// uptime-kuma-api) to a monitor before it is sent: notificationIDList may
// be given as a list of ids, and an empty accepted_statuscodes means
// ["200-299"].
func ConvertMonitorInput(m map[string]any) {
	if codes, _ := m["accepted_statuscodes"].([]any); len(codes) == 0 {
		m["accepted_statuscodes"] = []any{"200-299"}
	}
	switch ids := m["notificationIDList"].(type) {
	case []any:
		dict := make(map[string]any, len(ids))
		for _, id := range ids {
			dict[fmt.Sprint(id)] = true
		}
		m["notificationIDList"] = dict
	case nil:
		m["notificationIDList"] = map[string]any{}
	}
}

const secretChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// genSecret is the UI's genSecret: n random letters and digits.
func genSecret(n int) (string, error) {
	out := make([]byte, 0, n)
	var buf [64]byte
	for len(out) < n {
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("kuma: random token: %w", err)
		}
		for _, c := range buf {
			// 248 is the largest multiple of 62 below 256: no modulo bias.
			if c < 248 && len(out) < n {
				out = append(out, secretChars[int(c)%len(secretChars)])
			}
		}
	}
	return string(out), nil
}
