package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/store"
)

// Request and response bodies. Their names are the component names of
// 2.0's OpenAPI document.

// Health is the body of GET /health.
type Health struct {
	Status  string `json:"status" example:"ok"`
	Version string `json:"version"`
}

// Token is the body of POST /login/access-token.
type Token struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type" default:"bearer"`
}

// UserCreate is the body of POST /users (documentation only: the body is
// validated by parseUserCreate, with 2.0's messages).
type UserCreate struct {
	Username string   `json:"username" minLength:"3" maxLength:"20" pattern:"^[A-Za-z0-9_.-]+$"`
	Password string   `json:"password" minLength:"8" format:"password" doc:"At most 72 bytes (bcrypt's limit)."`
	_        struct{} `json:"-" additionalProperties:"true"`
}

// loginBody documents the login form (OAuth2 password grant).
type loginBody struct {
	GrantType    string   `json:"grant_type,omitempty" pattern:"^password$"`
	Username     string   `json:"username"`
	Password     string   `json:"password" format:"password"`
	Scope        string   `json:"scope,omitempty" default:""`
	ClientID     string   `json:"client_id,omitempty"`
	ClientSecret string   `json:"client_secret,omitempty" format:"password"`
	_            struct{} `json:"-" additionalProperties:"true"`
}

// UserResponse is a user as the API shows it.
type UserResponse struct {
	ID        int64     `json:"id"`
	Username  string    `json:"username"`
	CreatedAt Timestamp `json:"created_at"`
	LastVisit Timestamp `json:"last_visit"`
}

// Message is the body of DELETE /users/{username}.
type Message struct {
	Message string `json:"message"`
}

// HTTPError is the body of an error answer other than 422.
type HTTPError struct {
	Detail string `json:"detail"`
}

// HTTPValidationError is the body of a 422 answer.
type HTTPValidationError struct {
	Detail []ValidationError `json:"detail"`
}

// ValidationError is one problem in a 422 answer.
type ValidationError struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input,omitempty"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

// Timestamp is a UTC time written as 2.0 writes it:
// "2026-11-13T15:43:41.808921Z", without the fraction when it is zero.
type Timestamp time.Time

// MarshalJSON implements json.Marshaler.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	u := time.Time(t).UTC()
	s := u.Format("2006-01-02T15:04:05")
	if us := u.Nanosecond() / 1000; us != 0 {
		s += fmt.Sprintf(".%06d", us)
	}
	return []byte(`"` + s + `Z"`), nil
}

// Schema implements huma.SchemaProvider.
func (Timestamp) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: "string", Format: "date-time"}
}

func userResponse(u store.User) UserResponse {
	return UserResponse{ID: u.ID, Username: u.Username, CreatedAt: Timestamp(u.CreatedAt), LastVisit: Timestamp(u.LastVisit)}
}

type (
	healthOutput  struct{ Body Health }
	tokenOutput   struct{ Body Token }
	userOutput    struct{ Body UserResponse }
	usersOutput   struct{ Body []UserResponse }
	messageOutput struct{ Body Message }
	usernameParam struct {
		Username string `path:"username"`
	}
	rawBodyInput struct {
		ContentType string `header:"Content-Type" hidden:"true"`
		RawBody     []byte
	}
)

func (s *Server) routes(maxBody int64) {
	secured := []map[string][]string{{securityScheme: {}}}
	op := func(method, path, id, summary, tag string, auth bool) huma.Operation {
		o := huma.Operation{
			Method: method, Path: path, OperationID: id, Summary: summary, Tags: []string{tag},
			MaxBodyBytes: maxBody,
		}
		if auth {
			o.Security = secured
		}
		return o
	}

	// GET / redirects to the docs page (not documented, as in 2.0).
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/docs")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	s.mux.HandleFunc("GET /redoc", s.redoc)
	s.mux.HandleFunc("/", s.notFound)

	huma.Register(s.api, op(http.MethodGet, "/health", "health_health_get", "Liveness probe", "Health", false),
		func(context.Context, *struct{}) (*healthOutput, error) {
			return &healthOutput{Body: Health{Status: "ok", Version: s.version}}, nil
		})

	huma.Register(s.api, op(http.MethodPost, "/login/access-token", "login_access_token_login_access_token_post", "Get an access token", "Authentication", false),
		s.login)
	s.documentBody("/login/access-token", http.MethodPost, "application/x-www-form-urlencoded",
		"Body_login_access_token_login_access_token_post", reflect.TypeFor[loginBody]())

	create := op(http.MethodPost, "/users", "create_user_users_post", "Create a user (admin only)", "Users", true)
	create.DefaultStatus = http.StatusCreated
	huma.Register(s.api, create, s.createUser)
	s.documentBody("/users", http.MethodPost, "application/json", "UserCreate", reflect.TypeFor[UserCreate]())

	huma.Register(s.api, op(http.MethodGet, "/users", "get_users_users_get", "List users (admin only)", "Users", true), s.listUsers)
	huma.Register(s.api, op(http.MethodGet, "/users/me", "get_me_users_me_get", "Get the current user", "Users", true), s.me)
	huma.Register(s.api, op(http.MethodGet, "/users/{username}", "get_user_users__username__get", "Get a user (admin only)", "Users", true), s.getUser)
	huma.Register(s.api, op(http.MethodDelete, "/users/{username}", "delete_user_users__username__delete", "Delete a user (admin only)", "Users", true), s.deleteUser)

	s.documentValidationErrors()
}

// redocPage is FastAPI's ReDoc page (get_redoc_html), which 2.0 serves at
// /redoc: it loads ReDoc from the same CDN and reads /openapi.json.
const redocPage = `<!DOCTYPE html>
<html>
<head>
<title>Uptime-Kuma-API - ReDoc</title>
<!-- needed for adaptive design -->
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1">
<link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
<link rel="shortcut icon" href="https://fastapi.tiangolo.com/img/favicon.png">
<!--
ReDoc doesn't change outer page styles
-->
<style>
  body {
    margin: 0;
    padding: 0;
  }
</style>
</head>
<body>
<noscript>
    ReDoc requires Javascript to function. Please enable it to browse the documentation.
</noscript>
<redoc spec-url="/openapi.json"></redoc>
<script src="https://cdn.jsdelivr.net/npm/redoc@2/bundles/redoc.standalone.js"> </script>
</body>
</html>
`

func (s *Server) redoc(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, redocPage)
}

// documentBody replaces the "binary" request body huma documents for a raw
// body with the schema of t, registered under name.
func (s *Server) documentBody(path, method, contentType, name string, t reflect.Type) {
	registry := s.api.OpenAPI().Components.Schemas
	schema := registry.Schema(t, false, name)
	registry.Map()[name] = schema
	o := operations(s.api.OpenAPI().Paths[path])[method]
	o.RequestBody = &huma.RequestBody{Content: map[string]*huma.MediaType{
		contentType: {Schema: &huma.Schema{Ref: "#/components/schemas/" + name}},
	}}
}

// documentValidationErrors documents the 422 answer of every operation
// that reads a body or a path parameter, as 2.0's document does, and the
// {"detail": "..."} body of the other errors in place of huma's default
// error model, which this API never sends.
func (s *Server) documentValidationErrors() {
	registry := s.api.OpenAPI().Components.Schemas
	validation := registry.Schema(reflect.TypeFor[HTTPValidationError](), true, "HTTPValidationError")
	httpError := registry.Schema(reflect.TypeFor[HTTPError](), true, "HTTPError")
	for _, item := range s.api.OpenAPI().Paths {
		for _, o := range operations(item) {
			o.Responses["default"] = &huma.Response{
				Description: "Error",
				Content:     map[string]*huma.MediaType{"application/json": {Schema: httpError}},
			}
			if o.RequestBody == nil && len(o.Parameters) == 0 {
				continue
			}
			o.Responses["422"] = &huma.Response{
				Description: "Validation Error",
				Content:     map[string]*huma.MediaType{"application/json": {Schema: validation}},
			}
		}
	}
	delete(registry.Map(), "ErrorModel")
	delete(registry.Map(), "ErrorDetail")
}

// operations returns the operations of a path by method.
func operations(item *huma.PathItem) map[string]*huma.Operation {
	ops := map[string]*huma.Operation{}
	for m, o := range map[string]*huma.Operation{
		http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
		http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
	} {
		if o != nil {
			ops[m] = o
		}
	}
	return ops
}

func currentUser(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

func requireAdmin(ctx context.Context) error {
	u, ok := currentUser(ctx)
	if !ok {
		return errNotAuthenticated() // unreachable behind withAuthentication
	}
	if u.Username != AdminUsername {
		return errAdminRequired()
	}
	return nil
}

func (s *Server) internal(ctx context.Context, what string, err error) error {
	s.log.ErrorContext(ctx, what, "request_id", infoFrom(ctx).id, "error", err)
	return errInternal()
}

func (s *Server) login(ctx context.Context, in *rawBodyInput) (*tokenOutput, error) {
	form, aerr := parseLoginForm(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	var hash string
	user, err := s.users.User(ctx, form.Username)
	switch {
	case err == nil:
		hash = user.PasswordHash
	case !errors.Is(err, store.ErrNotFound):
		return nil, s.internal(ctx, "look up user", err)
	}
	// An unknown user is checked against a dummy hash, so that the answer
	// takes as long as for a wrong password.
	if !s.passwords.Verify(form.Password, hash) {
		s.log.InfoContext(ctx, "failed login", "request_id", infoFrom(ctx).id, "username", form.Username)
		return nil, errBadCredentials()
	}
	if err := s.users.TouchLastVisit(ctx, user.Username); err != nil {
		if errors.Is(err, store.ErrNotFound) { // deleted meanwhile
			return nil, errBadCredentials()
		}
		return nil, s.internal(ctx, "update last visit", err)
	}
	return &tokenOutput{Body: Token{AccessToken: s.tokens.Issue(user.Username), TokenType: "bearer"}}, nil
}

func (s *Server) createUser(ctx context.Context, in *rawBodyInput) (*userOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	username, password, aerr := parseUserCreate(in.ContentType, in.RawBody)
	if aerr != nil {
		return nil, aerr
	}
	// Checked before hashing, so that a duplicate costs no bcrypt; the
	// insert below still catches a race.
	if _, err := s.users.User(ctx, username); err == nil {
		return nil, newError(http.StatusConflict, "Username already exists")
	}
	hash, err := s.passwords.Hash(password)
	if err != nil {
		return nil, s.internal(ctx, "hash password", err)
	}
	u, err := s.users.CreateUser(ctx, username, hash)
	if errors.Is(err, store.ErrExists) {
		return nil, newError(http.StatusConflict, "Username already exists")
	}
	if err != nil {
		return nil, s.internal(ctx, "create user", err)
	}
	s.log.InfoContext(ctx, "created user", "request_id", infoFrom(ctx).id, "username", username)
	return &userOutput{Body: userResponse(u)}, nil
}

func (s *Server) listUsers(ctx context.Context, _ *struct{}) (*usersOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	users, err := s.users.Users(ctx)
	if err != nil {
		return nil, s.internal(ctx, "list users", err)
	}
	out := make([]UserResponse, 0, len(users))
	for _, u := range users {
		out = append(out, userResponse(u))
	}
	return &usersOutput{Body: out}, nil
}

func (s *Server) me(ctx context.Context, _ *struct{}) (*userOutput, error) {
	u, ok := currentUser(ctx)
	if !ok {
		return nil, errNotAuthenticated()
	}
	return &userOutput{Body: userResponse(u)}, nil
}

// errPathNotFound is 2.0's answer for a username containing "/" (sent as
// %2F): its router matches no route.
func errPathNotFound() *apiError { return newError(http.StatusNotFound, "Not Found") }

func (s *Server) getUser(ctx context.Context, in *usernameParam) (*userOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if strings.Contains(in.Username, "/") {
		return nil, errPathNotFound()
	}
	u, err := s.users.User(ctx, in.Username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newError(http.StatusNotFound, fmt.Sprintf("User %s not found", in.Username))
	}
	if err != nil {
		return nil, s.internal(ctx, "get user", err)
	}
	return &userOutput{Body: userResponse(u)}, nil
}

func (s *Server) deleteUser(ctx context.Context, in *usernameParam) (*messageOutput, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if strings.Contains(in.Username, "/") {
		return nil, errPathNotFound()
	}
	if in.Username == AdminUsername {
		return nil, newError(http.StatusBadRequest, "The admin user cannot be deleted")
	}
	err := s.users.DeleteUser(ctx, in.Username)
	if errors.Is(err, store.ErrNotFound) {
		return nil, newError(http.StatusNotFound, fmt.Sprintf("User %s not found", in.Username))
	}
	if err != nil {
		return nil, s.internal(ctx, "delete user", err)
	}
	s.log.InfoContext(ctx, "deleted user", "request_id", infoFrom(ctx).id, "username", in.Username)
	return &messageOutput{Body: Message{Message: "Deleted user " + in.Username}}, nil
}
