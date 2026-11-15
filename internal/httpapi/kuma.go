package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// Kuma is what the routes need from Uptime Kuma; *kuma.Client implements
// it. Reads of pushed lists go to State after Sync.
type Kuma interface {
	Sync(ctx context.Context) error
	State() *kuma.State

	AddMonitor(ctx context.Context, monitor map[string]any) (json.RawMessage, error)
	GetMonitor(ctx context.Context, id int64) (json.RawMessage, error)
	EditMonitor(ctx context.Context, monitor map[string]any) (json.RawMessage, error)
	DeleteMonitor(ctx context.Context, id int64) (json.RawMessage, error)
	PauseMonitor(ctx context.Context, id int64) (json.RawMessage, error)
	ResumeMonitor(ctx context.Context, id int64) (json.RawMessage, error)
	MonitorBeats(ctx context.Context, id int64, hours float64) (json.RawMessage, error)
	AddMonitorTag(ctx context.Context, tagID, monitorID int64, value string) (json.RawMessage, error)
	DeleteMonitorTag(ctx context.Context, tagID, monitorID int64, value string) (json.RawMessage, error)

	Tags(ctx context.Context) (json.RawMessage, error)
	AddTag(ctx context.Context, tag map[string]any) (json.RawMessage, error)
	EditTag(ctx context.Context, tag map[string]any) (json.RawMessage, error)
	DeleteTag(ctx context.Context, id int64) (json.RawMessage, error)

	SaveNotification(ctx context.Context, notification map[string]any, id *int64) (json.RawMessage, error)
	TestNotification(ctx context.Context, notification map[string]any) (json.RawMessage, error)
	DeleteNotification(ctx context.Context, id int64) (json.RawMessage, error)
	SaveProxy(ctx context.Context, proxy map[string]any, id *int64) (json.RawMessage, error)
	DeleteProxy(ctx context.Context, id int64) (json.RawMessage, error)
	SaveDockerHost(ctx context.Context, host map[string]any, id *int64) (json.RawMessage, error)
	TestDockerHost(ctx context.Context, host map[string]any) (json.RawMessage, error)
	DeleteDockerHost(ctx context.Context, id int64) (json.RawMessage, error)

	AddMaintenance(ctx context.Context, m map[string]any) (json.RawMessage, error)
	GetMaintenance(ctx context.Context, id int64) (json.RawMessage, error)
	EditMaintenance(ctx context.Context, m map[string]any) (json.RawMessage, error)
	DeleteMaintenance(ctx context.Context, id int64) (json.RawMessage, error)
	PauseMaintenance(ctx context.Context, id int64) (json.RawMessage, error)
	ResumeMaintenance(ctx context.Context, id int64) (json.RawMessage, error)
	MaintenanceMonitors(ctx context.Context, id int64) (json.RawMessage, error)
	SetMaintenanceMonitors(ctx context.Context, id int64, monitors []map[string]any) (json.RawMessage, error)

	GetStatusPage(ctx context.Context, slug string) (json.RawMessage, error)
	PublicStatusPage(ctx context.Context, slug string) (json.RawMessage, error)
	AddStatusPage(ctx context.Context, title, slug string) (json.RawMessage, error)
	SaveStatusPage(ctx context.Context, slug string, config map[string]any, icon string, groups []any) (json.RawMessage, error)
	DeleteStatusPage(ctx context.Context, slug string, id int64) (json.RawMessage, error)
	PostIncident(ctx context.Context, slug string, incident map[string]any) (json.RawMessage, error)
	UnpinIncident(ctx context.Context, slug string) (json.RawMessage, error)

	DatabaseSize(ctx context.Context) (json.RawMessage, error)
	ShrinkDatabase(ctx context.Context) (json.RawMessage, error)
}

var _ Kuma = (*kuma.Client)(nil)

// Uptime Kuma's messages, translated to the English texts of its UI
// (src/lang/en.json of 2.5.11) when it sends a translation key: 2.0's
// clients got these texts from Uptime Kuma 1.x.
var kumaMessages = map[string]string{
	"successAdded":              "Added Successfully.",
	"successDeleted":            "Deleted Successfully.",
	"successPaused":             "Paused Successfully.",
	"successResumed":            "Resumed Successfully.",
	"successEdited":             "Edited Successfully.",
	"successEnabled":            "Enabled Successfully.",
	"successDisabled":           "Disabled Successfully.",
	"successAuthChangePassword": "Password has been updated successfully.",
	"tagNotFound":               "Tag not found.",
}

func kumaMessage(msg string, i18n bool) string {
	if t, ok := kumaMessages[msg]; ok && i18n {
		return t
	}
	return msg
}

// decodeObject decodes a JSON object keeping numbers as written.
func decodeObject(raw json.RawMessage) (map[string]any, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if dec.Decode(&m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// ackBody is an ack as 2.0 returned it (uptime-kuma-api drops "ok"), with
// the message in English.
func ackBody(ack json.RawMessage) map[string]any {
	m, ok := decodeObject(ack)
	if !ok {
		return map[string]any{}
	}
	delete(m, "ok")
	if msg, ok := m["msg"].(string); ok {
		m["msg"] = kumaMessage(msg, m["msgi18n"] == true)
	}
	delete(m, "msgi18n")
	return m
}

// How Uptime Kuma answers for an unknown id on some commands: a raw
// JavaScript or SQL error. 2.0 (app/errors.py) answers a plain "Not found".
var (
	notFoundRaw = regexp.MustCompile(`(?i)Cannot read properties of null|No slug\?|You do not own this|FOREIGN KEY constraint failed`)
	notFound    = regexp.MustCompile(`(?i)does not exist|not found|no .*found`)
	// Other raw errors are internal details: logged, never returned.
	rawError = regexp.MustCompile(`(?i)Cannot read propert|is not a function|is not defined|is not iterable|SQLITE_|constraint failed|Unexpected token`)
)

// kumaError maps an error of a call to Uptime Kuma to the answer 2.0 gave,
// and logs it with the request id.
func (s *Server) kumaError(ctx context.Context, err error) error {
	id := infoFrom(ctx).id
	var ce *kuma.CommandError
	switch {
	case errors.As(err, &ce):
		msg := kumaMessage(ce.Msg, ce.I18n)
		s.log.InfoContext(ctx, "Uptime Kuma refused a command", "request_id", id, "event", ce.Event, "msg", ce.Msg)
		switch {
		case notFoundRaw.MatchString(msg):
			return newError(http.StatusNotFound, "Not found")
		case notFound.MatchString(msg):
			return newError(http.StatusNotFound, msg)
		case rawError.MatchString(msg), msg == "":
			return newError(http.StatusBadRequest, "Uptime Kuma could not process the request")
		}
		return newError(http.StatusBadRequest, msg)
	case errors.Is(err, kuma.ErrNotFound):
		return errNotFound("Not found")
	case errors.Is(err, kuma.ErrTimeout):
		s.log.WarnContext(ctx, "Uptime Kuma did not answer in time", "request_id", id, "error", err)
		return newError(http.StatusGatewayTimeout, "Uptime Kuma did not answer in time")
	case errors.Is(err, kuma.ErrUnavailable):
		s.log.ErrorContext(ctx, "Uptime Kuma is unavailable", "request_id", id, "error", err)
		switch {
		case errors.Is(err, kuma.ErrTwoFactorRequired):
			return newError(http.StatusServiceUnavailable, "the Uptime Kuma account requires two-factor authentication, which the API cannot use")
		case errors.Is(err, kuma.ErrLoginRejected):
			return newError(http.StatusServiceUnavailable, "Uptime Kuma rejected the login")
		}
		return newError(http.StatusServiceUnavailable, "Uptime Kuma is unavailable")
	}
	return s.internal(ctx, "call Uptime Kuma", err)
}

func errNotFound(detail string) *apiError { return newError(http.StatusNotFound, detail) }

// rawJSON is a response body already encoded. T only documents its schema
// in the OpenAPI document.
type rawJSON[T any] json.RawMessage

// MarshalJSON implements json.Marshaler.
func (r rawJSON[T]) MarshalJSON() ([]byte, error) { return r, nil }

// Schema implements huma.SchemaProvider.
func (rawJSON[T]) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeFor[T](), true, "")
}

type jsonOutput[T any] struct{ Body rawJSON[T] }

// replyAs encodes v as the response body.
func replyAs[T any](s *Server, ctx context.Context, v any) (*jsonOutput[T], error) {
	b, err := marshalJSON(v)
	if err != nil {
		return nil, s.internal(ctx, "encode the response", err)
	}
	return &jsonOutput[T]{Body: b}, nil
}

// bodyInput is the input of a route that reads a JSON body.
type bodyInput struct {
	ContentType string `header:"Content-Type" hidden:"true"`
	RawBody     []byte
}

// Inputs with a path parameter and a body. Huma only reads the parameters
// of the input struct itself, not of embedded structs.

func (in *monitorBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *tagBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *notificationBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *proxyBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *dockerHostBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *maintenanceBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}
func (in *slugBodyInput) body() *bodyInput {
	return &bodyInput{ContentType: in.ContentType, RawBody: in.RawBody}
}

// decodeModel decodes and checks a body against model m.
func decodeModel(m *model, in *bodyInput) (map[string]any, *apiError) {
	v, aerr := decodeJSONBody(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	return checkModel(m, v)
}

// decodeDict decodes a body that must be an object, then checks the
// documented fields of m.
func decodeDict(m *model, in *bodyInput) (map[string]any, *apiError) {
	v, aerr := decodeJSONBody(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	obj, aerr := checkDict(v)
	if aerr != nil {
		return nil, aerr
	}
	return checkModel(m, obj)
}

// required returns a copy of m in which the named fields are required.
func (m *model) required(names ...string) *model {
	c := &model{fields: append([]modelField(nil), m.fields...)}
	for i := range c.fields {
		for _, n := range names {
			if c.fields[i].name == n {
				c.fields[i].required = true
			}
		}
	}
	return c
}

// missingArguments is 2.0's TypeError for arguments missing (or null) in
// the object sent to Uptime Kuma: a 422 with a text detail.
func missingArguments(obj map[string]any, names ...string) *apiError {
	var missing []string
	for _, n := range names {
		if obj[n] == nil {
			missing = append(missing, "'"+n+"'")
		}
	}
	if missing == nil {
		return nil
	}
	return newError(http.StatusUnprocessableEntity, "missing "+strconv.Itoa(len(missing))+" required argument: "+strings.Join(missing, ", "))
}

// number returns v as a float64 when it is a JSON number.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// checkRange is 2.0's _check_argument_conditions for one field.
func checkRange(obj map[string]any, name string, minimum, maximum *float64) *apiError {
	n, ok := number(obj[name])
	if !ok {
		return nil
	}
	if minimum != nil && n < *minimum {
		return newError(http.StatusUnprocessableEntity, "the value of "+name+" must not be less than "+trimFloat(*minimum))
	}
	if maximum != nil && n > *maximum {
		return newError(http.StatusUnprocessableEntity, "the value of "+name+" must not be larger than "+trimFloat(*maximum))
	}
	return nil
}

func trimFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func ptr[T any](v T) *T { return &v }
