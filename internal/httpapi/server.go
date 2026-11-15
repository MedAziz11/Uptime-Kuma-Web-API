// Package httpapi is the REST API: routes, request and response types,
// middleware, error mapping and the OpenAPI document (huma v2 on the
// standard library's ServeMux).
//
// The contract is 2.0's: same paths, status codes, bodies, headers and
// error format ({"detail": ...}, with FastAPI's list of validation errors
// for 422).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/auth"
	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// Users is the user store; *store.Store implements it. User and Users must
// be cheap: they run on every authenticated request.
type Users interface {
	User(ctx context.Context, username string) (store.User, error)
	Users(ctx context.Context) ([]store.User, error)
	CreateUser(ctx context.Context, username, passwordHash string) (store.User, error)
	DeleteUser(ctx context.Context, username string) error
	TouchLastVisit(ctx context.Context, username string) error
}

// AdminUsername is the user that manages users and cannot be deleted.
const AdminUsername = "admin"

// DefaultMaxBodyBytes bounds request bodies unless Options says otherwise.
const DefaultMaxBodyBytes = 1 << 20

// Options configures a Server. Users, Passwords, Tokens and Kuma are
// required.
type Options struct {
	Users        Users
	Passwords    *auth.Passwords
	Tokens       *auth.Tokens
	Kuma         Kuma
	CORSOrigins  []string
	MaxBodyBytes int64        // default DefaultMaxBodyBytes
	Logger       *slog.Logger // default: discard
	Version      string       // reported by /health and the OpenAPI document
}

// Server is the HTTP handler of the API.
type Server struct {
	users     Users
	passwords *auth.Passwords
	tokens    *auth.Tokens
	kuma      Kuma
	log       *slog.Logger
	version   string

	mux     *http.ServeMux
	api     huma.API
	handler http.Handler
}

// New builds the API.
func New(opts Options) (*Server, error) {
	if opts.Users == nil || opts.Passwords == nil || opts.Tokens == nil || opts.Kuma == nil {
		return nil, errors.New("httpapi: Users, Passwords, Tokens and Kuma are required")
	}
	if opts.MaxBodyBytes <= 0 {
		opts.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{
		users:     opts.Users,
		passwords: opts.Passwords,
		tokens:    opts.Tokens,
		kuma:      opts.Kuma,
		log:       opts.Logger,
		version:   opts.Version,
		mux:       http.NewServeMux(),
	}
	s.api = humago.New(s.mux, s.humaConfig())
	s.routes(opts.MaxBodyBytes)

	// Outermost first: request id, panic recovery, access log, CORS, body
	// size limit, authentication (architecture, section 6).
	var h http.Handler = s.mux
	h = s.withAuthentication(h)
	h = withBodyLimit(opts.MaxBodyBytes, h)
	h = withCORS(opts.CORSOrigins, h)
	h = withAccessLog(s.log, h)
	h = withRecovery(s.log, h)
	h = withRequestID(h)
	s.handler = h
	return s, nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// OpenAPI returns the OpenAPI document.
func (s *Server) OpenAPI() *huma.OpenAPI { return s.api.OpenAPI() }

// securityScheme is the name 2.0 (FastAPI's OAuth2PasswordBearer) gives it.
const securityScheme = "OAuth2PasswordBearer"

func (s *Server) humaConfig() huma.Config {
	format := huma.Format{
		Marshal: func(w io.Writer, v any) error {
			b, err := marshalJSON(v)
			if err != nil {
				return err
			}
			_, err = w.Write(b)
			return err
		},
		Unmarshal: json.Unmarshal,
	}
	return huma.Config{
		OpenAPI: &huma.OpenAPI{
			OpenAPI: "3.1.0",
			Info: &huma.Info{
				Title:       "Uptime-Kuma-API",
				Version:     s.version,
				Description: "A REST API wrapper for Uptime Kuma.",
				License:     &huma.License{Name: "MIT", Identifier: "MIT"},
			},
			Components: &huma.Components{
				Schemas: huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer),
				SecuritySchemes: map[string]*huma.SecurityScheme{
					securityScheme: {
						Type: "oauth2",
						Flows: &huma.OAuthFlows{Password: &huma.OAuthFlow{
							TokenURL: "/login/access-token",
							Scopes:   map[string]string{},
						}},
					},
				},
			},
		},
		OpenAPIPath:  "/openapi",
		DocsPath:     "/docs",
		DocsRenderer: huma.DocsRendererSwaggerUI,
		// FastAPI's Swagger UI settings in 2.0.
		DocsRendererConfig: map[string]any{"deepLinking": true, "showExtensions": true, "showCommonExtensions": true},
		Formats:            map[string]huma.Format{"application/json": format, "json": format},
		DefaultFormat:      "application/json",
		Transformers:       []huma.Transformer{errorTransformer},
	}
}

// notFound answers what no route matches, like 2.0 (Starlette): 405 with
// Allow when the path exists for other methods, a 307 redirect when the
// path exists with or without a trailing slash, else 404.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if allowed := s.allowedMethods(r.URL); len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, newError(http.StatusMethodNotAllowed, "Method Not Allowed"))
		return
	}
	if p := r.URL.Path; p != "/" {
		alt := *r.URL
		if strings.HasSuffix(p, "/") {
			alt.Path = strings.TrimSuffix(p, "/")
		} else {
			alt.Path = p + "/"
		}
		alt.RawPath = ""
		if len(s.allowedMethods(&alt)) > 0 {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			target := url.URL{Scheme: scheme, Host: r.Host, Path: alt.Path, RawQuery: r.URL.RawQuery}
			w.Header().Set("Location", target.String())
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
	}
	writeError(w, newError(http.StatusNotFound, "Not Found"))
}

// allowedMethods lists the methods a route serves for u's path.
func (s *Server) allowedMethods(u *url.URL) []string {
	var allowed []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := &http.Request{Method: m, URL: u, Host: "", Header: http.Header{}}
		if _, pattern := s.mux.Handler(probe); pattern != "" && pattern != "/" {
			allowed = append(allowed, m)
		}
	}
	return allowed
}
