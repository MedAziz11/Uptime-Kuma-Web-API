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
