package httpapi

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// Maintenances, status pages, and the statistics and server routes.

type (
	maintenanceIDParam struct {
		MaintenanceID int64 `path:"maintenance_id"`
	}
	maintenanceBodyInput struct {
		MaintenanceID int64  `path:"maintenance_id"`
		ContentType   string `header:"Content-Type" hidden:"true"`
		RawBody       []byte
	}
	slugParam struct {
		Slug string `path:"slug"`
	}
	slugBodyInput struct {
		Slug        string `path:"slug"`
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
)

// Maintenances.

func (s *Server) listMaintenances(ctx context.Context, _ *struct{}) (*jsonOutput[Maintenances], error) {
	list, err := s.objects(ctx, kuma.Maintenances)
	if err != nil {
		return nil, err
	}
	return replyAs[Maintenances](s, ctx, map[string]any{"maintenances": views(list, rawView)})
}

// maintenance reads a maintenance from Uptime Kuma (2.0's shape), after
// checking that it exists.
func (s *Server) maintenance(ctx context.Context, id int64) (map[string]any, error) {
	if _, err := s.object(ctx, kuma.Maintenances, id, "Not found"); err != nil {
		return nil, err
	}
	ack, err := s.kuma.GetMaintenance(ctx, id)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Maintenance json.RawMessage `json:"maintenance"`
	}
	_ = json.Unmarshal(ack, &got)
	m, ok := decodeObject(got.Maintenance)
	if !ok {
		return nil, s.internal(ctx, "read the maintenance", errUnexpected)
	}
	return m, nil
}

func (s *Server) getMaintenance(ctx context.Context, in *maintenanceIDParam) (*jsonOutput[MaintenanceResponse], error) {
	m, err := s.maintenance(ctx, in.MaintenanceID)
	if err != nil {
		return nil, err
	}
	return replyAs[MaintenanceResponse](s, ctx, map[string]any{"maintenance": m})
}

// checkMaintenance is 2.0's _check_arguments_maintenance.
func checkMaintenance(m map[string]any) *apiError {
	if aerr := missingArguments(m, "title", "strategy"); aerr != nil {
		return aerr
	}
	switch m["strategy"] {
	case "recurring-interval", "recurring-weekday", "recurring-day-of-month":
		if aerr := missingArguments(m, "dateRange"); aerr != nil {
			return aerr
		}
	}
	return checkRange(m, "intervalDay", ptr(1.0), ptr(3650.0))
}

func (s *Server) createMaintenance(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	fields, aerr := decodeModel(maintenanceCreateModel, in)
	if aerr != nil {
		return nil, aerr
	}
	// 2.0's defaults (_build_maintenance_data).
	m := map[string]any{
		"active": true, "description": "", "intervalDay": 1, "weekdays": []any{}, "daysOfMonth": []any{},
		"timeRange":       []any{map[string]any{"hours": 2, "minutes": 0}, map[string]any{"hours": 3, "minutes": 0}},
		"cron":            "30 3 * * *",
		"durationMinutes": 60, "timezoneOption": nil,
	}
	for k, v := range fields {
		if empty(v) && m[k] != nil {
			continue // 2.0 replaced empty lists and null by the default
		}
		m[k] = v
	}
	if empty(m["dateRange"]) {
		m["dateRange"] = []any{time.Now().Format("2006-01-02") + " 00:00:00"}
	}
	if aerr := checkMaintenance(m); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.AddMaintenance(ctx, m))
}

// empty reports a null or an empty list, which 2.0's builders replaced by
// their default.
func empty(v any) bool {
	l, isList := v.([]any)
	return v == nil || isList && len(l) == 0
}

func (s *Server) updateMaintenance(ctx context.Context, in *maintenanceBodyInput) (*jsonOutput[MaintenanceUpdated], error) {
	changes, aerr := decodeModel(maintenanceUpdateModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	m, err := s.maintenance(ctx, in.MaintenanceID)
	if err != nil {
		return nil, err
	}
	maps.Copy(m, changes)
	if aerr := checkMaintenance(m); aerr != nil {
		return nil, aerr
	}
	ack, err := s.kuma.EditMaintenance(ctx, m)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	body := ackBody(ack)
	body["maintenance_data"] = changes
	return replyAs[MaintenanceUpdated](s, ctx, body)
}

func (s *Server) deleteMaintenance(ctx context.Context, in *maintenanceIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.Maintenances, in.MaintenanceID, "maintenance does not exist"); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteMaintenance(ctx, in.MaintenanceID))
}

func (s *Server) pauseMaintenance(ctx context.Context, in *maintenanceIDParam) (*jsonOutput[KumaMessage], error) {
	return s.answer(ctx)(s.kuma.PauseMaintenance(ctx, in.MaintenanceID))
}

func (s *Server) resumeMaintenance(ctx context.Context, in *maintenanceIDParam) (*jsonOutput[KumaMessage], error) {
	return s.answer(ctx)(s.kuma.ResumeMaintenance(ctx, in.MaintenanceID))
}

func (s *Server) maintenanceMonitors(ctx context.Context, in *maintenanceIDParam) (*jsonOutput[MonitorMaintenanceList], error) {
	ack, err := s.kuma.MaintenanceMonitors(ctx, in.MaintenanceID)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Monitors []json.RawMessage `json:"monitors"`
	}
	if err := json.Unmarshal(ack, &got); err != nil {
		return nil, s.internal(ctx, "read the monitors of a maintenance", err)
	}
	if got.Monitors == nil {
		got.Monitors = []json.RawMessage{}
	}
	return replyAs[MonitorMaintenanceList](s, ctx, got.Monitors)
}

func (s *Server) setMaintenanceMonitors(ctx context.Context, in *maintenanceBodyInput) (*jsonOutput[KumaMessage], error) {
	v, aerr := decodeJSONBody(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	list, aerr := checkModelList(monitorMaintenanceModel, v)
	if aerr != nil {
		return nil, aerr
	}
	// Uptime Kuma would link monitors to a maintenance that does not exist.
	if _, err := s.object(ctx, kuma.Maintenances, in.MaintenanceID, "Not found"); err != nil {
		return nil, err
	}
	monitors := make([]map[string]any, len(list))
	for i, m := range list {
		monitors[i] = map[string]any{"id": m["id"]}
	}
	return s.answer(ctx)(s.kuma.SetMaintenanceMonitors(ctx, in.MaintenanceID, monitors))
}

// Status pages.

func (s *Server) listStatusPages(ctx context.Context, _ *struct{}) (*jsonOutput[StatusPageList], error) {
	list, err := s.objects(ctx, kuma.StatusPages)
	if err != nil {
		return nil, err
	}
	return replyAs[StatusPageList](s, ctx, map[string]any{"statuspages": views(list, rawView)})
}

// statusPageID returns the id of the status page with slug, from the state.
func (s *Server) statusPageID(ctx context.Context, slug, detail string) (int64, error) {
	list, err := s.objects(ctx, kuma.StatusPages)
	if err != nil {
		return 0, err
	}
	for _, p := range list {
		var page struct {
			ID   int64  `json:"id"`
			Slug string `json:"slug"`
		}
		if json.Unmarshal(p, &page) == nil && page.Slug == slug {
			return page.ID, nil
		}
	}
	return 0, errNotFound(detail)
}

func (s *Server) createStatusPage(ctx context.Context, in *bodyInput) (*jsonOutput[AddStatusPageResponse], error) {
	p, aerr := decodeModel(addStatusPageModel, in)
	if aerr != nil {
		return nil, aerr
	}
	title, _ := p["title"].(string)
	slug, _ := p["slug"].(string)
	ack, err := s.kuma.AddStatusPage(ctx, title, slug)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	// 2.0's response model keeps only msg.
	return replyAs[AddStatusPageResponse](s, ctx, map[string]any{"msg": ackBody(ack)["msg"]})
}

// statusPage is the configuration of a status page and what its visitors
// see, merged as 2.0 did.
type statusPage struct {
	config    map[string]any
	incidents []any
	groups    []any
	maint     []any
}

func (s *Server) readStatusPage(ctx context.Context, slug string) (*statusPage, error) {
	ack, err := s.kuma.GetStatusPage(ctx, slug)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var priv struct {
		Config json.RawMessage `json:"config"`
	}
	_ = json.Unmarshal(ack, &priv)
	config, ok := decodeObject(priv.Config)
	if !ok {
		return nil, s.internal(ctx, "read the status page", errUnexpected)
	}
	pub, err := s.kuma.PublicStatusPage(ctx, slug)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	p, ok := decodeObject(pub)
	if !ok {
		return nil, s.internal(ctx, "read the public status page", errUnexpected)
	}
	if c, ok := p["config"].(map[string]any); ok {
		maps.Copy(config, c)
	}
	page := &statusPage{config: config, incidents: list(p["incidents"]), groups: list(p["publicGroupList"]), maint: list(p["maintenanceList"])}
	return page, nil
}

func list(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{}
}

func (s *Server) getStatusPage(ctx context.Context, in *slugParam) (*jsonOutput[StatusPage], error) {
	page, err := s.readStatusPage(ctx, in.Slug)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(page.config)
	// Uptime Kuma 2.x lists the pinned incidents, newest first; 2.0's
	// clients know the one pinned incident.
	out["incident"] = nil
	if len(page.incidents) > 0 {
		out["incident"] = page.incidents[0]
	}
	for _, g := range page.groups {
		if gm, ok := g.(map[string]any); ok {
			for _, m := range list(gm["monitorList"]) {
				if mm, ok := m.(map[string]any); ok {
					intToBool(mm, "sendUrl")
				}
			}
		}
	}
	out["publicGroupList"] = page.groups
	out["maintenanceList"] = page.maint
	return replyAs[StatusPage](s, ctx, out)
}

func (s *Server) saveStatusPage(ctx context.Context, in *slugBodyInput) (*jsonOutput[Detail], error) {
	changes, aerr := decodeModel(saveStatusPageModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	page, err := s.readStatusPage(ctx, in.Slug)
	if err != nil {
		return nil, err
	}
	config := page.config
	icon, _ := config["icon"].(string)
	groups := page.groups
	for k, v := range changes {
		switch k {
		case "icon":
			icon, _ = v.(string)
		case "publicGroupList":
			groups = list(v)
		case "googleAnalyticsId":
			// Uptime Kuma 2.x generalised Google Analytics to analyticsId
			// and analyticsType.
			config["analyticsId"] = v
			if config["analyticsType"] == nil && v != nil {
				config["analyticsType"] = "google"
			}
		default:
			config[k] = v
		}
	}
	// 2.0's _build_status_page_data.
	switch t, _ := config["theme"].(string); t {
	case "":
		config["theme"] = "auto"
	case "auto", "light", "dark":
	default:
		return nil, newError(http.StatusUnprocessableEntity, "theme must be auto, light or dark")
	}
	if icon == "" {
		icon = "/icon.svg"
	}
	ack, err := s.kuma.SaveStatusPage(ctx, in.Slug, config, icon, groups)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[Detail](s, ctx, map[string]any{"detail": ackBody(ack)})
}

func (s *Server) deleteStatusPage(ctx context.Context, in *slugParam) (*jsonOutput[Detail], error) {
	id, err := s.statusPageID(ctx, in.Slug, "status page does not exist")
	if err != nil {
		return nil, err
	}
	if _, err := s.kuma.DeleteStatusPage(ctx, in.Slug, id); err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[Detail](s, ctx, map[string]any{"detail": "success"})
}

func (s *Server) postIncident(ctx context.Context, in *slugBodyInput) (*jsonOutput[KumaObject], error) {
	inc, aerr := decodeModel(postIncidentModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	style, ok := inc["style"].(string)
	if !ok {
		style = "primary"
	}
	ack, err := s.kuma.PostIncident(ctx, in.Slug, map[string]any{"title": inc["title"], "content": inc["content"], "style": style})
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[KumaObject](s, ctx, ackBody(ack)["incident"])
}

func (s *Server) unpinIncident(ctx context.Context, in *slugParam) (*jsonOutput[Detail], error) {
	// Uptime Kuma answers success for any slug.
	if _, err := s.statusPageID(ctx, in.Slug, "Not found"); err != nil {
		return nil, err
	}
	if _, err := s.kuma.UnpinIncident(ctx, in.Slug); err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[Detail](s, ctx, map[string]any{"detail": "success"})
}

// Server, database and statistics.

func (s *Server) info(ctx context.Context, _ *struct{}) (*jsonOutput[ServerInfo], error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	info, snap := st.Info()
	snap.Synced = snap.Synced && info != nil
	if err := s.synced(ctx, snap); err != nil {
		return nil, err
	}
	return replyAs[ServerInfo](s, ctx, info)
}

// byMonitor writes a map keyed by monitor id.
func byMonitor[V any](m map[int64]V) map[string]V {
	out := make(map[string]V, len(m))
	for id, v := range m {
		out[strconv.FormatInt(id, 10)] = v
	}
	return out
}

// statistics answers a map by monitor read from State.
func statistics[V any](s *Server, ctx context.Context, read func(*kuma.State) (map[int64]V, kuma.Snapshot)) (*jsonOutput[ByMonitor], error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	m, snap := read(st)
	if err := s.synced(ctx, snap); err != nil {
		return nil, err
	}
	return replyAs[ByMonitor](s, ctx, byMonitor(m))
}

func (s *Server) avgPing(ctx context.Context, _ *struct{}) (*jsonOutput[ByMonitor], error) {
	return statistics(s, ctx, (*kuma.State).AvgPing)
}

func (s *Server) uptime(ctx context.Context, _ *struct{}) (*jsonOutput[ByMonitor], error) {
	return statistics(s, ctx, (*kuma.State).Uptime)
}

func (s *Server) certInfo(ctx context.Context, _ *struct{}) (*jsonOutput[ByMonitor], error) {
	return statistics(s, ctx, (*kuma.State).CertInfo)
}

func (s *Server) databaseSize(ctx context.Context, _ *struct{}) (*jsonOutput[DatabaseSize], error) {
	ack, err := s.kuma.DatabaseSize(ctx)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	body := ackBody(ack)
	body["unit"] = "octet"
	return replyAs[DatabaseSize](s, ctx, body)
}

func (s *Server) shrinkDatabase(ctx context.Context, _ *struct{}) (*jsonOutput[DatabaseShrink], error) {
	ack, err := s.kuma.ShrinkDatabase(ctx)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[DatabaseShrink](s, ctx, map[string]any{"message": "DB Shrinked", "details": ackBody(ack)})
}

// errBackupImportGone is the answer of POST /settings/upload_backup.
func errBackupImportGone() *apiError {
	return newError(http.StatusGone, "Uptime Kuma 2.x removed backup import (its uploadBackup command no longer exists), "+
		"so this API cannot import backups any more. Recreate the objects with the API, or migrate Uptime Kuma's database instead.")
}

func (s *Server) uploadBackup(context.Context, *struct{}) (*struct{}, error) {
	return nil, errBackupImportGone()
}
