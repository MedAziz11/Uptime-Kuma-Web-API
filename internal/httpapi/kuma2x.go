package httpapi

import (
	"context"
	"encoding/json"
	"maps"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// Resources of Uptime Kuma 2.x that 2.0 did not expose.

type (
	apiKeyIDParam struct {
		APIKeyID int64 `path:"api_key_id"`
	}
	chartInput struct {
		MonitorID int64   `path:"monitor_id"`
		Period    float64 `query:"period" default:"24" exclusiveMinimum:"0" doc:"Hours, greater than 0: by minute up to 24, by hour up to 720, else by day."`
	}
	importantBeatsInput struct {
		MonitorID int64 `path:"monitor_id"`
		Offset    int64 `query:"offset" default:"0" minimum:"0"`
		Limit     int64 `query:"limit" default:"25" minimum:"1" maximum:"1000"`
	}
	pushExampleInput struct {
		Language string `path:"language" doc:"For example javascript-fetch, python, bash-curl."`
	}
	incidentHistoryInput struct {
		Slug   string `path:"slug"`
		Cursor string `query:"cursor" doc:"The nextCursor of the previous page; empty for the first page."`
	}
	incidentParam struct {
		Slug       string `path:"slug"`
		IncidentID int64  `path:"incident_id"`
	}
	incidentBodyInput struct {
		Slug        string `path:"slug"`
		IncidentID  int64  `path:"incident_id"`
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
	remoteBrowserIDParam struct {
		RemoteBrowserID int64 `path:"remote_browser_id"`
	}
	remoteBrowserBodyInput struct {
		RemoteBrowserID int64  `path:"remote_browser_id"`
		ContentType     string `header:"Content-Type" hidden:"true"`
		RawBody         []byte
	}
)

// API keys (pushed by Uptime Kuma, served from State).

// apiKeyView is an API key with its flag as a boolean.
func apiKeyView(raw json.RawMessage) any {
	k, ok := decodeObject(raw)
	if !ok {
		return raw
	}
	intToBool(k, "active")
	return k
}

const apiKeyNotFound = "API key does not exist"

func (s *Server) createAPIKey(ctx context.Context, in *bodyInput) (*jsonOutput[APIKeyCreated], error) {
	fields, aerr := decodeDict(apiKeyModel, in)
	if aerr != nil {
		return nil, aerr
	}
	// The defaults of Uptime Kuma's dialog: active, never expires.
	k := map[string]any{"active": true, "expires": nil}
	maps.Copy(k, fields)
	if aerr := missingArguments(k, "name"); aerr != nil {
		return nil, aerr
	}
	ack, err := s.kuma.AddAPIKey(ctx, k)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[APIKeyCreated](s, ctx, ackBody(ack))
}

func (s *Server) enableAPIKey(ctx context.Context, in *apiKeyIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.APIKeys, in.APIKeyID, apiKeyNotFound); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.EnableAPIKey(ctx, in.APIKeyID))
}

func (s *Server) disableAPIKey(ctx context.Context, in *apiKeyIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.APIKeys, in.APIKeyID, apiKeyNotFound); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DisableAPIKey(ctx, in.APIKeyID))
}

func (s *Server) deleteAPIKey(ctx context.Context, in *apiKeyIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.APIKeys, in.APIKeyID, apiKeyNotFound); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteAPIKey(ctx, in.APIKeyID))
}

// Remote browsers (pushed by Uptime Kuma, served from State).

func (in *remoteBrowserBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}

const remoteBrowserNotFound = "remote browser does not exist"

func (s *Server) createRemoteBrowser(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	b, aerr := decodeDict(remoteBrowserModel, in)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := missingArguments(b, "name", "url"); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveRemoteBrowser(ctx, b, nil))
}

func (s *Server) testRemoteBrowser(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	b, aerr := decodeDict(remoteBrowserModel, in)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := missingArguments(b, "url"); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.TestRemoteBrowser(ctx, b))
}

func (s *Server) updateRemoteBrowser(ctx context.Context, in *remoteBrowserBodyInput) (*jsonOutput[KumaMessage], error) {
	changes, aerr := decodeDict(remoteBrowserModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	cur, err := s.object(ctx, kuma.RemoteBrowsers, in.RemoteBrowserID, remoteBrowserNotFound)
	if err != nil {
		return nil, err
	}
	b, ok := decodeObject(cur)
	if !ok {
		return nil, s.internal(ctx, "read the remote browser", errUnexpected)
	}
	maps.Copy(b, changes)
	if aerr := missingArguments(b, "name", "url"); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveRemoteBrowser(ctx, b, &in.RemoteBrowserID))
}

func (s *Server) deleteRemoteBrowser(ctx context.Context, in *remoteBrowserIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.RemoteBrowsers, in.RemoteBrowserID, remoteBrowserNotFound); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteRemoteBrowser(ctx, in.RemoteBrowserID))
}

// Incidents of a status page (not pushed: read from Uptime Kuma).

func (in *incidentBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}

// maxIncidentPages bounds the search of an incident in the history.
const maxIncidentPages = 1000

func (s *Server) incidentHistory(ctx context.Context, in *incidentHistoryInput) (*jsonOutput[IncidentHistory], error) {
	ack, err := s.kuma.IncidentHistory(ctx, in.Slug, in.Cursor)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[IncidentHistory](s, ctx, ackBody(ack))
}

// incident finds an incident in the history of a status page, or answers
// 404.
func (s *Server) incident(ctx context.Context, slug string, id int64) (map[string]any, error) {
	cursor := ""
	for range maxIncidentPages {
		ack, err := s.kuma.IncidentHistory(ctx, slug, cursor)
		if err != nil {
			return nil, s.kumaError(ctx, err)
		}
		var page struct {
			Incidents  []json.RawMessage `json:"incidents"`
			NextCursor *string           `json:"nextCursor"`
			HasMore    bool              `json:"hasMore"`
		}
		if err := json.Unmarshal(ack, &page); err != nil {
			return nil, s.internal(ctx, "read the incident history", err)
		}
		for _, raw := range page.Incidents {
			if iid, ok := idOf(raw); ok && iid == id {
				inc, _ := decodeObject(raw)
				return inc, nil
			}
		}
		if !page.HasMore || page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	// Uptime Kuma's cursor is a creation date, to the second: the history
	// ends after its first page when the incidents of the page's end were
	// created in the same second. The public page lists every pinned,
	// active incident.
	pub, err := s.kuma.PublicStatusPage(ctx, slug)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var page struct {
		Incidents []json.RawMessage `json:"incidents"`
	}
	_ = json.Unmarshal(pub, &page)
	for _, raw := range page.Incidents {
		if iid, ok := idOf(raw); ok && iid == id {
			inc, _ := decodeObject(raw)
			return inc, nil
		}
	}
	return nil, errNotFound("incident does not exist")
}

func (s *Server) updateIncident(ctx context.Context, in *incidentBodyInput) (*jsonOutput[IncidentSaved], error) {
	changes, aerr := decodeModel(incidentUpdateModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	inc, err := s.incident(ctx, in.Slug, in.IncidentID)
	if err != nil {
		return nil, err
	}
	maps.Copy(inc, changes)
	if aerr := missingArguments(inc, "title", "content"); aerr != nil {
		return nil, aerr
	}
	ack, err := s.kuma.EditIncident(ctx, in.Slug, in.IncidentID, inc)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[IncidentSaved](s, ctx, ackBody(ack))
}

func (s *Server) deleteIncident(ctx context.Context, in *incidentParam) (*jsonOutput[KumaMessage], error) {
	return s.answer(ctx)(s.kuma.DeleteIncident(ctx, in.Slug, in.IncidentID))
}

func (s *Server) resolveIncident(ctx context.Context, in *incidentParam) (*jsonOutput[IncidentSaved], error) {
	ack, err := s.kuma.ResolveIncident(ctx, in.Slug, in.IncidentID)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[IncidentSaved](s, ctx, ackBody(ack))
}

// Chart data and important heartbeats of a monitor (read from Uptime Kuma;
// it answers an unknown monitor with empty data, so State is checked first).

func (s *Server) monitorChartData(ctx context.Context, in *chartInput) (*jsonOutput[ChartData], error) {
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	ack, err := s.kuma.MonitorChartData(ctx, in.MonitorID, in.Period)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(ack, &got); err != nil || got.Data == nil {
		return nil, s.internal(ctx, "read the chart data", errUnexpected)
	}
	return replyAs[ChartData](s, ctx, map[string]any{"chart_data": got.Data})
}

func (s *Server) importantBeats(ctx context.Context, in *importantBeatsInput) (*jsonOutput[ImportantBeats], error) {
	if _, err := s.monitor(ctx, in.MonitorID, "Not found"); err != nil {
		return nil, err
	}
	var count struct {
		Count *int64 `json:"count"`
	}
	ack, err := s.kuma.ImportantHeartbeatCount(ctx, in.MonitorID)
	if err == nil && (json.Unmarshal(ack, &count) != nil || count.Count == nil) {
		return nil, s.internal(ctx, "read the heartbeat count", errUnexpected)
	}
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var page struct {
		Data json.RawMessage `json:"data"`
	}
	ack, err = s.kuma.ImportantHeartbeats(ctx, in.MonitorID, in.Offset, in.Limit)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	if err := json.Unmarshal(ack, &page); err != nil || page.Data == nil {
		return nil, s.internal(ctx, "read the heartbeats", errUnexpected)
	}
	return replyAs[ImportantBeats](s, ctx, map[string]any{"important_beats": page.Data, "total": *count.Count})
}

// pushExample answers Uptime Kuma's example code for a push monitor.
func (s *Server) pushExample(ctx context.Context, in *pushExampleInput) (*jsonOutput[PushExample], error) {
	ack, err := s.kuma.PushExample(ctx, in.Language)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[PushExample](s, ctx, ackBody(ack))
}
