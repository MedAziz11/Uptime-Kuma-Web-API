package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

type (
	monitorIDParam struct {
		MonitorID int64 `path:"monitor_id"`
	}
	monitorBodyInput struct {
		monitorIDParam
		bodyInput
	}
	beatsInput struct {
		monitorIDParam
		Hours float64 `query:"hours" default:"1" exclusiveMinimum:"0" doc:"Hours, greater than 0."`
	}
)

// monitorView is a monitor as 2.0 returned it (uptime-kuma-api's
// _convert_monitor_return, int_to_bool and parse_auth_method):
// notificationIDList is a list of ids, active a boolean, a null authMethod
// is "".
func monitorView(raw json.RawMessage) any {
	m, ok := decodeObject(raw)
	if !ok {
		return raw
	}
	if ids, ok := m["notificationIDList"].(map[string]any); ok {
		list := make([]int64, 0, len(ids))
		for k := range ids {
			if id, err := strconv.ParseInt(k, 10, 64); err == nil {
				list = append(list, id)
			}
		}
		slices.Sort(list)
		m["notificationIDList"] = list
	}
	intToBool(m, "active")
	if v, ok := m["authMethod"]; ok && v == nil {
		m["authMethod"] = ""
	}
	return m
}

// intToBool is uptime-kuma-api's int_to_bool: 1 is true, any other value
// false.
func intToBool(m map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			n, isNum := number(v)
			m[k] = v == true || isNum && n == 1
		}
	}
}

func (s *Server) listMonitors(ctx context.Context, _ *struct{}) (*jsonOutput[Monitors], error) {
	if err := s.kuma.Sync(ctx); err != nil {
		return nil, s.kumaError(ctx, err)
	}
	list := s.kuma.State().Objects(kuma.Monitors)
	out := make([]any, len(list))
	for i, m := range list {
		out[i] = monitorView(m)
	}
	return replyAs[Monitors](s, ctx, map[string]any{"monitors": out})
}

// monitor returns a monitor from the state, or 2.0's 404.
func (s *Server) monitor(ctx context.Context, id int64, detail string) (json.RawMessage, error) {
	if err := s.kuma.Sync(ctx); err != nil {
		return nil, s.kumaError(ctx, err)
	}
	m, ok := s.kuma.State().Object(kuma.Monitors, id)
	if !ok {
		return nil, errNotFound(detail)
	}
	return m, nil
}

func (s *Server) getMonitor(ctx context.Context, in *monitorIDParam) (*jsonOutput[MonitorResponse], error) {
	m, err := s.monitor(ctx, in.MonitorID, "Not found")
	if err != nil {
		return nil, err
	}
	return replyAs[MonitorResponse](s, ctx, map[string]any{"monitor": monitorView(m)})
}

func (s *Server) createMonitor(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	fields, aerr := decodeModel(monitorCreateModel, in)
	if aerr != nil {
		return nil, aerr
	}
	m, err := kuma.NewMonitor(fields)
	if err != nil {
		return nil, s.internal(ctx, "build the monitor", err)
	}
	if aerr := checkMonitor(m); aerr != nil {
		return nil, aerr
	}
	ack, err := s.kuma.AddMonitor(ctx, m)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[KumaMessage](s, ctx, ackBody(ack))
}

func (s *Server) updateMonitor(ctx context.Context, in *monitorBodyInput) (*jsonOutput[MonitorUpdated], error) {
	changes, aerr := decodeModel(monitorUpdateModel, &in.bodyInput)
	if aerr != nil {
		return nil, aerr
	}
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	// The full monitor, sensitive fields included, from the database.
	ack, err := s.kuma.GetMonitor(ctx, in.MonitorID)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Monitor json.RawMessage `json:"monitor"`
	}
	if err := json.Unmarshal(ack, &got); err != nil {
		return nil, s.internal(ctx, "read the monitor", err)
	}
	m, err := kuma.MergeMonitor(got.Monitor, changes)
	if err != nil {
		return nil, s.internal(ctx, "merge the monitor", err)
	}
	if aerr := checkMonitor(m); aerr != nil {
		return nil, aerr
	}
	ack, err = s.kuma.EditMonitor(ctx, m)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	body := ackBody(ack)
	body["monitor_data"] = changes
	return replyAs[MonitorUpdated](s, ctx, body)
}

func (s *Server) deleteMonitor(ctx context.Context, in *monitorIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.monitor(ctx, in.MonitorID, "monitor does not exist"); err != nil {
		return nil, err
	}
	return s.monitorCommand(ctx, s.kuma.DeleteMonitor, in.MonitorID)
}

func (s *Server) pauseMonitor(ctx context.Context, in *monitorIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	return s.monitorCommand(ctx, s.kuma.PauseMonitor, in.MonitorID)
}

func (s *Server) resumeMonitor(ctx context.Context, in *monitorIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	return s.monitorCommand(ctx, s.kuma.ResumeMonitor, in.MonitorID)
}

// monitorCommand runs a command on an id and answers its ack.
func (s *Server) monitorCommand(ctx context.Context, cmd func(context.Context, int64) (json.RawMessage, error), id int64) (*jsonOutput[KumaMessage], error) {
	ack, err := cmd(ctx, id)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[KumaMessage](s, ctx, ackBody(ack))
}

func (s *Server) monitorBeats(ctx context.Context, in *beatsInput) (*jsonOutput[MonitorBeats], error) {
	ack, err := s.kuma.MonitorBeats(ctx, in.MonitorID, in.Hours)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(ack, &got); err != nil {
		return nil, s.internal(ctx, "read the heartbeats", err)
	}
	beats := make([]any, 0, len(got.Data))
	for _, raw := range got.Data {
		b, ok := decodeObject(raw)
		if !ok {
			beats = append(beats, raw)
			continue
		}
		intToBool(b, "important")
		beats = append(beats, b)
	}
	return replyAs[MonitorBeats](s, ctx, map[string]any{"monitor_beats": beats})
}

// monitorTag decodes a MonitorTag body.
func monitorTag(in *bodyInput) (tagID int64, value string, _ *apiError) {
	obj, aerr := decodeModel(monitorTagModel, in)
	if aerr != nil {
		return 0, "", aerr
	}
	n, _ := obj["tag_id"].(json.Number)
	tagID, _ = n.Int64()
	value, _ = obj["value"].(string)
	return tagID, value, nil
}

func (s *Server) addMonitorTag(ctx context.Context, in *monitorBodyInput) (*jsonOutput[KumaMessage], error) {
	tagID, value, aerr := monitorTag(&in.bodyInput)
	if aerr != nil {
		return nil, aerr
	}
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	// Uptime Kuma would store a link to a tag that does not exist.
	if _, err := s.tag(ctx, tagID); err != nil {
		return nil, err
	}
	ack, err := s.kuma.AddMonitorTag(ctx, tagID, in.MonitorID, value)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[KumaMessage](s, ctx, ackBody(ack))
}

func (s *Server) deleteMonitorTag(ctx context.Context, in *monitorBodyInput) (*jsonOutput[KumaMessage], error) {
	tagID, value, aerr := monitorTag(&in.bodyInput)
	if aerr != nil {
		return nil, aerr
	}
	m, err := s.monitor(ctx, in.MonitorID, "monitor tag does not exist")
	if err != nil {
		return nil, err
	}
	var got struct {
		Tags []monitorTagLink `json:"tags"`
	}
	_ = json.Unmarshal(m, &got)
	if !slices.ContainsFunc(got.Tags, func(t monitorTagLink) bool {
		id, _ := t.TagID.Int64()
		return id == tagID && t.Value == value
	}) {
		return nil, errNotFound("monitor tag does not exist")
	}
	ack, err := s.kuma.DeleteMonitorTag(ctx, tagID, in.MonitorID, value)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[KumaMessage](s, ctx, ackBody(ack))
}

// monitorTagLink is an entry of a monitor's tags.
type monitorTagLink struct {
	TagID json.Number `json:"tag_id"`
	Value string      `json:"value"`
}

// checkMonitor is 2.0's _check_arguments_monitor on the object sent to
// Uptime Kuma: required arguments, ranges and enumerations, answered with
// a 422 and a text detail.
func checkMonitor(m map[string]any) *apiError {
	if aerr := missingArguments(m, "type", "name", "interval", "maxretries", "retryInterval"); aerr != nil {
		return aerr
	}
	typ, _ := m["type"].(string)
	if aerr := missingArguments(m, requiredByType(typ)...); aerr != nil {
		return aerr
	}
	for _, r := range []struct {
		name     string
		min, max *float64
	}{
		{"interval", ptr(20.0), nil}, {"maxretries", ptr(0.0), nil}, {"retryInterval", ptr(20.0), nil},
		{"maxredirects", ptr(0.0), nil}, {"port", ptr(0.0), ptr(65535.0)},
	} {
		if aerr := checkRange(m, r.name, r.min, r.max); aerr != nil {
			return aerr
		}
	}
	codes, _ := m["accepted_statuscodes"].([]any)
	for _, c := range codes {
		if !validStatusCode(c) {
			return newError(http.StatusUnprocessableEntity, "Unknown accepted_statuscodes value: "+jsonText(c))
		}
	}
	if t, ok := m["dns_resolve_type"]; ok && t != nil {
		if s, _ := t.(string); !slices.Contains([]string{"A", "AAAA", "CAA", "CNAME", "MX", "NS", "PTR", "SOA", "SRV", "TXT"}, s) {
			return newError(http.StatusUnprocessableEntity, "Unknown dns_resolve_type value: "+jsonText(t))
		}
	}
	if typ == "kafka-producer" {
		sasl, _ := m["kafkaProducerSaslOptions"].(map[string]any)
		if mech, _ := sasl["mechanism"].(string); !slices.Contains([]string{"None", "plain", "scram-sha-256", "scram-sha-512", "aws"}, mech) {
			return newError(http.StatusUnprocessableEntity, `Unknown kafkaProducerSaslOptions["mechanism"] value: `+jsonText(sasl["mechanism"]))
		}
	}
	return nil
}

// requiredByType lists the arguments 2.0 required for a monitor type
// beyond those with a default.
func requiredByType(typ string) []string {
	switch typ {
	case "http", "real-browser":
		return []string{"url"}
	case "port", "steam", "mqtt":
		return []string{"hostname", "port"}
	case "ping", "dns", "tailscale-ping":
		return []string{"hostname"}
	case "keyword":
		return []string{"url", "keyword"}
	case "grpc-keyword":
		return []string{"grpcUrl", "keyword", "grpcServiceName", "grpcMethod"}
	case "docker":
		return []string{"docker_container", "docker_host"}
	case "gamedig":
		return []string{"game", "hostname", "port"}
	case "radius":
		return []string{"radiusUsername", "radiusPassword", "radiusSecret", "radiusCalledStationId", "radiusCallingStationId"}
	case "json-query":
		return []string{"url", "jsonPath", "expectedValue"}
	case "kafka-producer":
		return []string{"kafkaProducerTopic", "kafkaProducerMessage"}
	}
	return nil
}

// validStatusCode accepts what 2.0 accepted ("200-299" ranges and 100 to
// 999) and WebSocket close codes (1000 to 4999), which Uptime Kuma 2.x
// uses for websocket-upgrade monitors.
func validStatusCode(v any) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	switch s {
	case "100-199", "200-299", "300-399", "400-499", "500-599":
		return true
	}
	n, err := strconv.Atoi(s)
	return err == nil && strconv.Itoa(n) == s && n >= 100 && n <= 4999
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "?"
	}
	return string(b)
}
