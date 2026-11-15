package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// Tags, notifications, proxies and docker hosts.

type (
	tagIDParam struct {
		TagID int64 `path:"tag_id"`
	}
	tagBodyInput struct {
		TagID       int64  `path:"tag_id"`
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
	notificationIDParam struct {
		NotificationID int64 `path:"notification_id"`
	}
	notificationBodyInput struct {
		NotificationID int64  `path:"notification_id"`
		ContentType    string `header:"Content-Type" hidden:"true"`
		RawBody        []byte
	}
	proxyIDParam struct {
		ProxyID int64 `path:"proxy_id"`
	}
	proxyBodyInput struct {
		ProxyID     int64  `path:"proxy_id"`
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
	dockerHostIDParam struct {
		DockerHostID int64 `path:"docker_host_id"`
	}
	dockerHostBodyInput struct {
		DockerHostID int64  `path:"docker_host_id"`
		ContentType  string `header:"Content-Type" hidden:"true"`
		RawBody      []byte
	}
)

// answer returns a function that turns the result of a command into the
// response: its ack as 2.0 returned it. Use: s.answer(ctx)(s.kuma.X(...)).
func (s *Server) answer(ctx context.Context) func(json.RawMessage, error) (*jsonOutput[KumaMessage], error) {
	return func(ack json.RawMessage, err error) (*jsonOutput[KumaMessage], error) {
		if err != nil {
			return nil, s.kumaError(ctx, err)
		}
		return replyAs[KumaMessage](s, ctx, ackBody(ack))
	}
}

// errUnexpected reports an object of Uptime Kuma that is not what it
// should be.
var errUnexpected = errors.New("unexpected object from Uptime Kuma")

// Tags (not pushed by Uptime Kuma: read with getTags).

func (s *Server) tags(ctx context.Context) ([]json.RawMessage, error) {
	ack, err := s.kuma.Tags(ctx)
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	var got struct {
		Tags []json.RawMessage `json:"tags"`
	}
	if err := json.Unmarshal(ack, &got); err != nil {
		return nil, s.internal(ctx, "read the tags", err)
	}
	return got.Tags, nil
}

// tag returns a tag, or 2.0's 404.
func (s *Server) tag(ctx context.Context, id int64) (map[string]any, error) {
	tags, err := s.tags(ctx)
	if err != nil {
		return nil, err
	}
	for _, raw := range tags {
		if tid, ok := idOf(raw); ok && tid == id {
			t, _ := decodeObject(raw)
			return t, nil
		}
	}
	return nil, errNotFound("tag does not exist")
}

func idOf(raw json.RawMessage) (int64, bool) {
	var o struct {
		ID json.Number `json:"id"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return 0, false
	}
	id, err := o.ID.Int64()
	return id, err == nil
}

func (s *Server) listTags(ctx context.Context, _ *struct{}) (*jsonOutput[Tags], error) {
	tags, err := s.tags(ctx)
	if err != nil {
		return nil, err
	}
	if tags == nil {
		tags = []json.RawMessage{}
	}
	return replyAs[Tags](s, ctx, map[string]any{"tags": tags})
}

func (s *Server) getTag(ctx context.Context, in *tagIDParam) (*jsonOutput[TagResponse], error) {
	t, err := s.tag(ctx, in.TagID)
	if err != nil {
		return nil, err
	}
	return replyAs[TagResponse](s, ctx, map[string]any{"tag": t})
}

func (s *Server) createTag(ctx context.Context, in *bodyInput) (*jsonOutput[TagObject], error) {
	t, aerr := decodeModel(tagModel, in)
	if aerr != nil {
		return nil, aerr
	}
	ack, err := s.kuma.AddTag(ctx, map[string]any{"new": true, "name": t["name"], "color": t["color"]})
	if err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return replyAs[TagObject](s, ctx, ackBody(ack)["tag"])
}

func (s *Server) updateTag(ctx context.Context, in *tagBodyInput) (*jsonOutput[KumaMessage], error) {
	changes, aerr := decodeModel(tagUpdateModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	t, err := s.tag(ctx, in.TagID)
	if err != nil {
		return nil, err
	}
	for _, k := range []string{"name", "color"} {
		if v, ok := changes[k]; ok {
			t[k] = v
		}
	}
	if aerr := missingArguments(t, "name", "color"); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.EditTag(ctx, t))
}

func (s *Server) deleteTag(ctx context.Context, in *tagIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.tag(ctx, in.TagID); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteTag(ctx, in.TagID))
}

// state returns the State once the session is logged in.
func (s *Server) state(ctx context.Context) (*kuma.State, error) {
	if err := s.kuma.Sync(ctx); err != nil {
		return nil, s.kumaError(ctx, err)
	}
	return s.kuma.State(), nil
}

// synced is the answer for a read of State: an error unless State held a
// complete login state when it was read. The connection may drop between
// Sync and the read; an empty list must not be answered then.
func (s *Server) synced(ctx context.Context, snap kuma.Snapshot) error {
	if !snap.Synced {
		return s.kumaError(ctx, fmt.Errorf("%w: the connection was lost while reading", kuma.ErrUnavailable))
	}
	return nil
}

// objects returns a pushed list, once the session is logged in.
func (s *Server) objects(ctx context.Context, l kuma.List) ([]json.RawMessage, error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	list, snap := st.Objects(l)
	if err := s.synced(ctx, snap); err != nil {
		return nil, err
	}
	return list, nil
}

// object returns an object of a pushed list, or a 404 with detail.
func (s *Server) object(ctx context.Context, l kuma.List, id int64, detail string) (json.RawMessage, error) {
	st, err := s.state(ctx)
	if err != nil {
		return nil, err
	}
	o, ok, snap := st.Object(l, id)
	if err := s.synced(ctx, snap); err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotFound(detail)
	}
	return o, nil
}

// views applies view to each object.
func views(list []json.RawMessage, view func(json.RawMessage) any) []any {
	out := make([]any, len(list))
	for i, o := range list {
		out[i] = view(o)
	}
	return out
}

// Notifications.

// notificationView is a notification as 2.0 returned it: the row with its
// JSON "config" merged in.
func notificationView(raw json.RawMessage) any {
	n, ok := decodeObject(raw)
	if !ok {
		return raw
	}
	if cfg, isString := n["config"].(string); isString {
		if c, ok := decodeObject(json.RawMessage(cfg)); ok {
			delete(n, "config")
			maps.Copy(n, c)
		}
	}
	return n
}

func (s *Server) listNotifications(ctx context.Context, _ *struct{}) (*jsonOutput[Notifications], error) {
	list, err := s.objects(ctx, kuma.Notifications)
	if err != nil {
		return nil, err
	}
	return replyAs[Notifications](s, ctx, map[string]any{"notifications": views(list, notificationView)})
}

func (s *Server) getNotification(ctx context.Context, in *notificationIDParam) (*jsonOutput[NotificationResponse], error) {
	n, err := s.object(ctx, kuma.Notifications, in.NotificationID, "notification does not exist")
	if err != nil {
		return nil, err
	}
	return replyAs[NotificationResponse](s, ctx, map[string]any{"notification": notificationView(n)})
}

// newNotification checks a notification to add or test, as 2.0 did
// (_build_notification_data): name and type are required, unknown options
// are refused; isDefault and applyExisting default to false.
func (s *Server) newNotification(in *bodyInput) (map[string]any, *apiError) {
	n, aerr := decodeDict(notificationModel, in)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := missingArguments(n, "name", "type"); aerr != nil {
		return nil, aerr
	}
	for _, k := range slices.Sorted(maps.Keys(n)) {
		if !s.notificationOptions[k] {
			return nil, newError(http.StatusUnprocessableEntity, "unknown argument '"+k+"'")
		}
	}
	for _, k := range []string{"isDefault", "applyExisting"} {
		if _, ok := n[k]; !ok {
			n[k] = false
		}
	}
	return n, nil
}

func (s *Server) createNotification(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	n, aerr := s.newNotification(in)
	if aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveNotification(ctx, n, nil))
}

func (s *Server) testNotification(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	n, aerr := s.newNotification(in)
	if aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.TestNotification(ctx, n))
}

func (s *Server) updateNotification(ctx context.Context, in *notificationBodyInput) (*jsonOutput[KumaMessage], error) {
	changes, aerr := decodeDict(notificationModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	cur, err := s.object(ctx, kuma.Notifications, in.NotificationID, "notification does not exist")
	if err != nil {
		return nil, err
	}
	n, _ := notificationView(cur).(map[string]any)
	if n == nil {
		return nil, s.internal(ctx, "read the notification", errUnexpected)
	}
	maps.Copy(n, changes)
	if aerr := missingArguments(n, "name", "type"); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveNotification(ctx, n, &in.NotificationID))
}

func (s *Server) deleteNotification(ctx context.Context, in *notificationIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.Notifications, in.NotificationID, "notification does not exist"); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteNotification(ctx, in.NotificationID))
}

// Proxies.

// proxyView is a proxy as 2.0 returned it: its flags as booleans.
func proxyView(raw json.RawMessage) any {
	p, ok := decodeObject(raw)
	if !ok {
		return raw
	}
	intToBool(p, "auth", "active", "default", "applyExisting")
	return p
}

func (s *Server) listProxies(ctx context.Context, _ *struct{}) (*jsonOutput[Proxies], error) {
	list, err := s.objects(ctx, kuma.Proxies)
	if err != nil {
		return nil, err
	}
	return replyAs[Proxies](s, ctx, map[string]any{"proxies": views(list, proxyView)})
}

func (s *Server) getProxy(ctx context.Context, in *proxyIDParam) (*jsonOutput[ProxyResponse], error) {
	p, err := s.object(ctx, kuma.Proxies, in.ProxyID, "proxy does not exist")
	if err != nil {
		return nil, err
	}
	return replyAs[ProxyResponse](s, ctx, map[string]any{"proxy": proxyView(p)})
}

// checkProxy is 2.0's _check_arguments_proxy.
func checkProxy(p map[string]any) *apiError {
	required := []string{"protocol", "host", "port"}
	if p["auth"] == true {
		required = append(required, "username", "password")
	}
	if aerr := missingArguments(p, required...); aerr != nil {
		return aerr
	}
	return checkRange(p, "port", ptr(0.0), ptr(65535.0))
}

func (s *Server) createProxy(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	fields, aerr := decodeDict(proxyModel, in)
	if aerr != nil {
		return nil, aerr
	}
	// 2.0's defaults (_build_proxy_data).
	p := map[string]any{"auth": false, "username": nil, "password": nil, "active": true, "default": false, "applyExisting": false}
	maps.Copy(p, fields)
	if aerr := checkProxy(p); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveProxy(ctx, p, nil))
}

func (s *Server) updateProxy(ctx context.Context, in *proxyBodyInput) (*jsonOutput[KumaMessage], error) {
	changes, aerr := decodeDict(proxyModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	cur, err := s.object(ctx, kuma.Proxies, in.ProxyID, "proxy does not exist")
	if err != nil {
		return nil, err
	}
	p, _ := proxyView(cur).(map[string]any)
	if p == nil {
		return nil, s.internal(ctx, "read the proxy", errUnexpected)
	}
	maps.Copy(p, changes)
	if aerr := checkProxy(p); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveProxy(ctx, p, &in.ProxyID))
}

func (s *Server) deleteProxy(ctx context.Context, in *proxyIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.Proxies, in.ProxyID, "proxy does not exist"); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteProxy(ctx, in.ProxyID))
}

// Docker hosts.

func rawView(raw json.RawMessage) any { return raw }

func (s *Server) listDockerHosts(ctx context.Context, _ *struct{}) (*jsonOutput[DockerHosts], error) {
	list, err := s.objects(ctx, kuma.DockerHosts)
	if err != nil {
		return nil, err
	}
	return replyAs[DockerHosts](s, ctx, map[string]any{"docker_hosts": views(list, rawView)})
}

func (s *Server) getDockerHost(ctx context.Context, in *dockerHostIDParam) (*jsonOutput[DockerHostResponse], error) {
	h, err := s.object(ctx, kuma.DockerHosts, in.DockerHostID, "docker host does not exist")
	if err != nil {
		return nil, err
	}
	return replyAs[DockerHostResponse](s, ctx, map[string]any{"docker_host": h})
}

// dockerHost completes a docker host as 2.0 did (_build_docker_host_data,
// _convert_docker_host_input): name and dockerType are required, and the
// daemon defaults by type.
func dockerHost(h map[string]any) *apiError {
	if aerr := missingArguments(h, "name", "dockerType"); aerr != nil {
		return aerr
	}
	if d, _ := h["dockerDaemon"].(string); d == "" {
		switch h["dockerType"] {
		case "socket":
			h["dockerDaemon"] = "/var/run/docker.sock"
		case "tcp":
			h["dockerDaemon"] = "tcp://localhost:2375"
		}
	}
	return nil
}

func (s *Server) newDockerHost(in *bodyInput) (map[string]any, *apiError) {
	h, aerr := decodeDict(dockerHostModel, in)
	if aerr != nil {
		return nil, aerr
	}
	if aerr := dockerHost(h); aerr != nil {
		return nil, aerr
	}
	return h, nil
}

func (s *Server) createDockerHost(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	h, aerr := s.newDockerHost(in)
	if aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveDockerHost(ctx, h, nil))
}

func (s *Server) testDockerHost(ctx context.Context, in *bodyInput) (*jsonOutput[KumaMessage], error) {
	h, aerr := s.newDockerHost(in)
	if aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.TestDockerHost(ctx, h))
}

func (s *Server) updateDockerHost(ctx context.Context, in *dockerHostBodyInput) (*jsonOutput[KumaMessage], error) {
	changes, aerr := decodeDict(dockerHostModel, in.body())
	if aerr != nil {
		return nil, aerr
	}
	cur, err := s.object(ctx, kuma.DockerHosts, in.DockerHostID, "docker host does not exist")
	if err != nil {
		return nil, err
	}
	h, ok := decodeObject(cur)
	if !ok {
		return nil, s.internal(ctx, "read the docker host", errUnexpected)
	}
	maps.Copy(h, changes)
	if aerr := dockerHost(h); aerr != nil {
		return nil, aerr
	}
	return s.answer(ctx)(s.kuma.SaveDockerHost(ctx, h, &in.DockerHostID))
}

func (s *Server) deleteDockerHost(ctx context.Context, in *dockerHostIDParam) (*jsonOutput[KumaMessage], error) {
	if _, err := s.object(ctx, kuma.DockerHosts, in.DockerHostID, "docker host does not exist"); err != nil {
		return nil, err
	}
	return s.answer(ctx)(s.kuma.DeleteDockerHost(ctx, in.DockerHostID))
}
