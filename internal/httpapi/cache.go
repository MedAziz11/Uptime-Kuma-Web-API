package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/kuma"
)

// Response cache of the reads served from State (architecture §5).
//
// Each route keeps the encoded body of its answer for one version of the
// list it reads, with a strong ETag (a hash of the bytes). A request checks
// the list's version under State's lock (kuma.State.Snapshot, which does not
// allocate) and writes the cached bytes when the version is the one they
// were built from and State is synced; otherwise the body is rebuilt, once:
// concurrent requests for a changed list wait for one construction. A body
// is built only from a synced read and every Reset bumps every version, so
// a body is never served for a version it was not built from, while State
// is unsynced, or after a reconnection. A write returns once the list's
// version advanced, so the next read rebuilds and sees it.
//
// The body is built lazily on the first read of a version, on the request's
// goroutine, not on the session's reader goroutine at push time: lists such
// as uptime and average ping change with every heartbeat, most versions are
// never read, and the reader must not be slowed down by encoding.
//
// Object lists keep the encoded view of each object (a sub-slice of the
// list's body) next to the object it was built from: a rebuild re-encodes
// only the objects that changed, and a single object (GET /monitors/{id})
// is written as a prefix, its piece of the list body and a suffix, with no
// copy and no allocation.

// The headers of a cached answer. These slices are assigned to response
// headers as they are and never modified.
var (
	jsonContentType = []string{"application/json"}
	// The answers are per user (authenticated): no shared cache may store
	// them, and clients revalidate them with If-None-Match every time,
	// since Uptime Kuma may push a change at any moment.
	cacheControl = []string{"private, no-cache"}
)

// cachedBody is an encoded answer and its headers.
type cachedBody struct {
	parts  [3][]byte // written in order
	etag   []string
	length []string
}

func newCachedBody(parts ...[]byte) cachedBody {
	var b cachedBody
	h := sha256.New()
	n := 0
	for i, p := range parts {
		b.parts[i] = p
		h.Write(p)
		n += len(p)
	}
	b.etag = []string{`"` + base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:18]) + `"`}
	b.length = []string{strconv.Itoa(n)}
	return b
}

// bodies is what one version of a list encodes to.
type bodies struct {
	version uint64
	list    cachedBody
	byID    map[int64]*cachedBody // single objects; nil for routes without them
	// raws are the State objects the pieces were encoded from.
	raws   map[int64]json.RawMessage
	pieces map[int64][]byte
}

// builder reads a list from State and encodes it. prev is the previous
// version's bodies (nil at first): unchanged objects reuse their piece. A
// result built from a read that was not synced must be discarded.
type builder func(st *kuma.State, prev *bodies) (*bodies, kuma.Snapshot, error)

// bodyCache is the cache of one route (or of a list and its objects).
type bodyCache struct {
	list  kuma.List
	build builder
	cur   atomic.Pointer[bodies]
	// building is held while a body is built: one construction per version.
	building chan struct{}
}

func newBodyCache(l kuma.List, build builder) *bodyCache {
	return &bodyCache{list: l, build: build, building: make(chan struct{}, 1)}
}

// get returns the bodies of the current version of the list, or false when
// State is not synced.
func (c *bodyCache) get(ctx context.Context, st *kuma.State) (*bodies, bool, error) {
	if b, synced := c.current(st); b != nil || !synced {
		return b, synced, nil
	}
	select {
	case c.building <- struct{}{}:
		defer func() { <-c.building }()
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	// Built by the request that held the lock?
	if b, synced := c.current(st); b != nil || !synced {
		return b, synced, nil
	}
	b, snap, err := c.build(st, c.cur.Load())
	if err != nil || !snap.Synced {
		return nil, false, err
	}
	b.version = snap.Version
	c.cur.Store(b)
	return b, true, nil
}

// current returns the cached bodies if they are of the list's current
// version.
func (c *bodyCache) current(st *kuma.State) (*bodies, bool) {
	snap := st.Snapshot(c.list)
	if !snap.Synced {
		return nil, false
	}
	if b := c.cur.Load(); b != nil && b.version == snap.Version {
		return b, true
	}
	return nil, true
}

// objectRoutes builds a list of objects answered as {"<listKey>": [...]},
// and each object as {"<objectKey>": ...} when objectKey is not empty.
func objectRoutes(l kuma.List, listKey, objectKey string, view func(json.RawMessage) any) *bodyCache {
	listPrefix, listSuffix := []byte(`{"`+listKey+`":[`), []byte(`]}`)
	objPrefix, objSuffix := []byte(`{"`+objectKey+`":`), []byte(`}`)
	return newBodyCache(l, func(st *kuma.State, prev *bodies) (*bodies, kuma.Snapshot, error) {
		ids, raws, snap := st.ObjectsByID(l)
		if !snap.Synced {
			return nil, snap, nil
		}
		pieces := make([][]byte, len(ids))
		size := len(listPrefix) + len(listSuffix) + len(ids)
		for i, id := range ids {
			if prev != nil && bytes.Equal(prev.raws[id], raws[i]) {
				pieces[i] = prev.pieces[id]
			} else {
				p, err := marshalJSON(view(raws[i]))
				if err != nil {
					return nil, snap, fmt.Errorf("encode %s %d: %w", l, id, err)
				}
				pieces[i] = p
			}
			size += len(pieces[i])
		}
		body := make([]byte, 0, size)
		body = append(body, listPrefix...)
		b := &bodies{raws: make(map[int64]json.RawMessage, len(ids)), pieces: make(map[int64][]byte, len(ids))}
		if objectKey != "" {
			b.byID = make(map[int64]*cachedBody, len(ids))
		}
		for i, id := range ids {
			if i > 0 {
				body = append(body, ',')
			}
			start := len(body)
			body = append(body, pieces[i]...)
			piece := body[start:len(body):len(body)]
			b.raws[id], b.pieces[id] = raws[i], piece
			if objectKey == "" {
				continue
			}
			if old := prev.object(id); old != nil && bytes.Equal(old.parts[1], piece) {
				reused := *old
				reused.parts[1] = piece
				b.byID[id] = &reused
				continue
			}
			o := newCachedBody(objPrefix, piece, objSuffix)
			b.byID[id] = &o
		}
		body = append(body, listSuffix...)
		b.list = newCachedBody(body)
		return b, snap, nil
	})
}

func (b *bodies) object(id int64) *cachedBody {
	if b == nil {
		return nil
	}
	return b.byID[id]
}

// wholeRoute builds an answer encoded as a whole from what read returns;
// ok false means that State has nothing to answer yet (like not synced).
func wholeRoute(l kuma.List, read func(st *kuma.State) (v any, ok bool, snap kuma.Snapshot)) *bodyCache {
	return newBodyCache(l, func(st *kuma.State, _ *bodies) (*bodies, kuma.Snapshot, error) {
		v, ok, snap := read(st)
		if !ok || !snap.Synced {
			snap.Synced = false
			return nil, snap, nil
		}
		body, err := marshalJSON(v)
		if err != nil {
			return nil, snap, fmt.Errorf("encode %s: %w", l, err)
		}
		return &bodies{list: newCachedBody(body)}, snap, nil
	})
}

// byMonitorRoute is a statistics route: a map by monitor id.
func byMonitorRoute[V any](l kuma.List, read func(*kuma.State) (map[int64]V, kuma.Snapshot)) *bodyCache {
	return wholeRoute(l, func(st *kuma.State) (any, bool, kuma.Snapshot) {
		m, snap := read(st)
		return byMonitor(m), true, snap
	})
}

// caches are the cached routes of a Server.
type caches struct {
	monitors, notifications, proxies, dockerHosts *bodyCache
	maintenances, statusPages, apiKeys            *bodyCache
	remoteBrowsers                                *bodyCache
	info, avgPing, uptime, certInfo               *bodyCache
}

func newCaches() *caches {
	return &caches{
		monitors:       objectRoutes(kuma.Monitors, "monitors", "monitor", monitorView),
		notifications:  objectRoutes(kuma.Notifications, "notifications", "notification", notificationView),
		proxies:        objectRoutes(kuma.Proxies, "proxies", "proxy", proxyView),
		dockerHosts:    objectRoutes(kuma.DockerHosts, "docker_hosts", "docker_host", rawView),
		maintenances:   objectRoutes(kuma.Maintenances, "maintenances", "", rawView),
		statusPages:    objectRoutes(kuma.StatusPages, "statuspages", "", rawView),
		apiKeys:        objectRoutes(kuma.APIKeys, "api_keys", "api_key", apiKeyView),
		remoteBrowsers: objectRoutes(kuma.RemoteBrowsers, "remote_browsers", "remote_browser", rawView),
		info: wholeRoute(kuma.Info, func(st *kuma.State) (any, bool, kuma.Snapshot) {
			info, snap := st.Info()
			return info, info != nil, snap
		}),
		avgPing:  byMonitorRoute(kuma.AvgPing, (*kuma.State).AvgPing),
		uptime:   byMonitorRoute(kuma.Uptime, (*kuma.State).Uptime),
		certInfo: byMonitorRoute(kuma.CertInfo, (*kuma.State).CertInfo),
	}
}

// cached returns the current bodies of c, once the session is logged in.
// While State is synced (the session is logged in) it does not call Sync,
// which allocates a deadline.
func (s *Server) cached(ctx context.Context, c *bodyCache) (*bodies, error) {
	st := s.kuma.State()
	b, synced, err := c.get(ctx, st)
	if err == nil && !synced {
		if err := s.kuma.Sync(ctx); err != nil {
			return nil, s.kumaError(ctx, err)
		}
		b, synced, err = c.get(ctx, st)
	}
	switch {
	case ctx.Err() != nil:
		return nil, s.kumaError(ctx, fmt.Errorf("%w: %w", kuma.ErrUnavailable, ctx.Err()))
	case err != nil:
		return nil, s.internal(ctx, "encode the response", err)
	case !synced:
		return nil, s.synced(ctx, kuma.Snapshot{})
	}
	return b, nil
}

// serveList answers a cached list.
func (s *Server) serveList(c *bodyCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := s.cached(r.Context(), c)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeCached(w, r, &b.list)
	}
}

// serveObject answers one object of a cached list, or a 404 with detail.
// The path parameter is checked as huma checked it before.
func (s *Server) serveObject(c *bodyCache, param, detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.PathValue(param)
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			writeError(w, errValidation(paramIssues([]*huma.ErrorDetail{{Location: "path." + param, Message: "invalid integer", Value: raw}})))
			return
		}
		b, err := s.cached(r.Context(), c)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		o := b.byID[id]
		if o == nil {
			writeError(w, errNotFound(detail))
			return
		}
		writeCached(w, r, o)
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	if e := (*apiError)(nil); errors.As(err, &e) {
		writeError(w, e)
		return
	}
	writeError(w, errInternal())
}

// writeCached writes b, or 304 Not Modified when the request's
// If-None-Match lists its ETag.
func writeCached(w http.ResponseWriter, r *http.Request, b *cachedBody) {
	h := w.Header()
	h["Etag"] = b.etag
	h["Cache-Control"] = cacheControl
	if etagMatch(r.Header["If-None-Match"], b.etag[0]) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h["Content-Type"] = jsonContentType
	h["Content-Length"] = b.length
	w.WriteHeader(http.StatusOK)
	for _, p := range b.parts {
		if len(p) > 0 {
			_, _ = w.Write(p)
		}
	}
}

// etagMatch is If-None-Match's weak comparison (RFC 9110, 13.1.2): true for
// "*" or when an entity tag of the list has etag's opaque tag, W/ or not.
// Malformed members are skipped.
func etagMatch(values []string, etag string) bool {
	for _, v := range values {
		for {
			v = strings.TrimLeft(v, " \t,")
			if v == "" {
				break
			}
			if v[0] == '*' {
				return true
			}
			v = strings.TrimPrefix(v, "W/")
			if v == "" {
				break
			}
			if v[0] != '"' {
				i := strings.IndexByte(v, ',')
				if i < 0 {
					break
				}
				v = v[i:]
				continue
			}
			end := strings.IndexByte(v[1:], '"')
			if end < 0 {
				break
			}
			if v[:end+2] == etag {
				return true
			}
			v = v[end+2:]
		}
	}
	return false
}
