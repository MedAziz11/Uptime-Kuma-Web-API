package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// apiError is every error response: {"detail": ...} with a status code and
// optional headers. detail is a string, or a list of validationIssue for
// 422 (the shape 2.0's FastAPI returns). Handlers return it; huma writes it
// through the JSON format, and middleware writes it with writeError.
type apiError struct {
	status  int
	Detail  any `json:"detail"`
	headers http.Header
}

func (e *apiError) Error() string {
	if s, ok := e.Detail.(string); ok {
		return s
	}
	return http.StatusText(e.status)
}

// GetStatus implements huma.StatusError.
func (e *apiError) GetStatus() int { return e.status }

// GetHeaders implements huma.HeadersError.
func (e *apiError) GetHeaders() http.Header { return e.headers }

func newError(status int, detail string) *apiError {
	return &apiError{status: status, Detail: detail}
}

// Errors with fixed texts, as 2.0 words them.
func errNotAuthenticated() *apiError {
	return &apiError{status: http.StatusUnauthorized, Detail: "Not authenticated", headers: bearerChallenge()}
}

func errInvalidToken() *apiError {
	return &apiError{status: http.StatusUnauthorized, Detail: "Invalid or expired token", headers: bearerChallenge()}
}

func errBadCredentials() *apiError {
	return &apiError{status: http.StatusUnauthorized, Detail: "Incorrect username or password", headers: bearerChallenge()}
}

func errAdminRequired() *apiError {
	return newError(http.StatusForbidden, "Admin privileges required")
}

func errInternal() *apiError {
	return newError(http.StatusInternalServerError, "Internal Server Error")
}

// errBodyParsing is 2.0's answer to a body it cannot decode at all.
func errBodyParsing() *apiError {
	return newError(http.StatusBadRequest, "There was an error parsing the body")
}

func bearerChallenge() http.Header {
	return http.Header{"Www-Authenticate": []string{"Bearer"}}
}

// validationIssue is one entry of a 422 answer, as pydantic/FastAPI write
// it.
type validationIssue struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}

func errValidation(issues []validationIssue) *apiError {
	return &apiError{status: http.StatusUnprocessableEntity, Detail: issues}
}

// writeError writes e outside huma (middleware, fallback handler).
func writeError(w http.ResponseWriter, e *apiError) {
	for k, vs := range e.headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	writeJSON(w, e.status, e)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := marshalJSON(v)
	if err != nil {
		status, body = http.StatusInternalServerError, []byte(`{"detail":"Internal Server Error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// errorTransformer turns the errors huma raises itself (for example a body
// over the size limit) into the {"detail": "..."} format.
func errorTransformer(ctx huma.Context, _ string, v any) (any, error) {
	if m, ok := v.(*huma.ErrorModel); ok {
		ctx.SetHeader("Content-Type", "application/json")
		return &apiError{status: m.Status, Detail: m.Detail}, nil
	}
	return v, nil
}
