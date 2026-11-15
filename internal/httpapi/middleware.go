package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// requestInfo is shared by the middleware of one request.
type requestInfo struct {
	id   string
	user string // set by authentication
}

type ctxKey int

const (
	infoKey ctxKey = iota
	userKey
)

func infoFrom(ctx context.Context) *requestInfo {
	if ri, ok := ctx.Value(infoKey).(*requestInfo); ok {
		return ri
	}
	return &requestInfo{}
}

// recorder remembers the status and size of a response.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the connection (huma sets read
// deadlines through it).
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withRequestID gives every request an id: the client's X-Request-ID when
// it is a plausible id, else a random one. It is echoed in the response.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if !validRequestID(id) {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), infoKey, &requestInfo{id: id})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, c := range []byte(id) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

// withRecovery turns a panic in a handler into a logged 500.
func withRecovery(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &recorder{ResponseWriter: w}
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if p == http.ErrAbortHandler {
				panic(p)
			}
			log.ErrorContext(r.Context(), "panic serving request",
				"request_id", infoFrom(r.Context()).id, "method", r.Method, "path", r.URL.Path,
				"panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			if rec.status == 0 {
				writeError(rec, errInternal())
			}
		}()
		next.ServeHTTP(rec, r)
	})
}

// withAccessLog logs one line per request.
func withAccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec, ok := w.(*recorder)
		if !ok {
			rec = &recorder{ResponseWriter: w}
		}
		defer func() {
			status := rec.status
			if p := recover(); p != nil {
				status = http.StatusInternalServerError
				defer panic(p)
			}
			info := infoFrom(r.Context())
			attrs := []slog.Attr{
				slog.String("request_id", info.id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.String("remote", r.RemoteAddr),
			}
			if info.user != "" {
				attrs = append(attrs, slog.String("user", info.user))
			}
			log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
		}()
		next.ServeHTTP(rec, r)
	})
}

// corsMethods is what 2.0 (Starlette, allow_methods=["*"]) allows.
var corsMethods = []string{"DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "QUERY"}

// withCORS applies BACKEND_CORS_ORIGINS exactly as 2.0's CORSMiddleware
// (allow_credentials, all methods, all headers, max age 600): preflight
// requests are answered here, other responses get the CORS headers.
func withCORS(origins []string, next http.Handler) http.Handler {
	if len(origins) == 0 {
		return next
	}
	allowAll := slices.Contains(origins, "*")
	allowed := func(origin string) bool { return allowAll || slices.Contains(origins, origin) }
	methods := strings.Join(corsMethods, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin, hasOrigin := headerValue(r.Header, "Origin")
		h := w.Header()
		requestedMethod, isPreflight := headerValue(r.Header, "Access-Control-Request-Method")
		if hasOrigin && r.Method == http.MethodOptions && isPreflight {
			h.Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers, Access-Control-Request-Private-Network")
			h.Set("Access-Control-Allow-Methods", methods)
			h.Set("Access-Control-Max-Age", "600")
			h.Set("Access-Control-Allow-Credentials", "true")
			var failures []string
			if allowed(origin) {
				h.Set("Access-Control-Allow-Origin", origin)
			} else {
				failures = append(failures, "origin")
			}
			if !slices.Contains(corsMethods, requestedMethod) {
				failures = append(failures, "method")
			}
			if hdrs, ok := headerValue(r.Header, "Access-Control-Request-Headers"); ok {
				h.Set("Access-Control-Allow-Headers", hdrs)
			}
			if _, ok := headerValue(r.Header, "Access-Control-Request-Private-Network"); ok {
				failures = append(failures, "private-network")
			}
			h.Set("Content-Type", "text/plain; charset=utf-8")
			if failures != nil {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Disallowed CORS " + strings.Join(failures, ", ")))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		if hasOrigin {
			h.Set("Access-Control-Allow-Credentials", "true")
			if allowed(origin) {
				h.Set("Access-Control-Allow-Origin", origin)
			}
		}
		h.Add("Vary", "Origin")
		next.ServeHTTP(w, r)
	})
}

// headerValue is Header.Get that also reports presence.
func headerValue(h http.Header, name string) (string, bool) {
	v, ok := h[http.CanonicalHeaderKey(name)]
	if !ok || len(v) == 0 {
		return "", false
	}
	return v[0], true
}

// withBodyLimit rejects bodies over limit bytes with 413.
func withBodyLimit(limit int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limit {
			writeError(w, newError(http.StatusRequestEntityTooLarge,
				"Request body too large: the limit is "+strconv.FormatInt(limit, 10)+" bytes"))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// publicPaths may be requested without a token, with any method (an
// unsupported method gets 405 there, as in 2.0). Everything else requires
// a valid token, including paths no route serves.
var publicPaths = []string{
	"/", "/health", "/login/access-token", "/docs", "/redoc",
	"/openapi.json", "/openapi.yaml", "/openapi-3.0.json", "/openapi-3.0.yaml",
}

// withAuthentication checks the bearer token of every non-public request
// and puts the user in the request context. Answers and their order follow
// 2.0 (OAuth2PasswordBearer, then get_current_user).
func (s *Server) withAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slices.Contains(publicPaths, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "bearer") {
			writeError(w, errNotAuthenticated())
			return
		}
		username, err := s.tokens.Subject(strings.TrimSpace(token))
		if err != nil {
			s.log.DebugContext(r.Context(), "token rejected", "request_id", infoFrom(r.Context()).id, "reason", err.Error())
			writeError(w, errInvalidToken())
			return
		}
		user, err := s.users.User(r.Context(), username)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.ErrorContext(r.Context(), "look up user", "request_id", infoFrom(r.Context()).id, "error", err)
				writeError(w, errInternal())
				return
			}
			writeError(w, errInvalidToken())
			return
		}
		infoFrom(r.Context()).user = user.Username
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}
