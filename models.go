package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// catalog is the decoded /v1/models response.
type catalog struct {
	Models      []pluginapi.ModelInfo
	Attestation string
}

// catalogResponse mirrors the gateway model catalog payload.
type catalogResponse struct {
	Data []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ContextLength int64  `json:"context_length"`
		MaxTokens     int64  `json:"max_tokens"`
		Vision        bool   `json:"vision"`
		Thinking      *struct {
			Levels     []string `json:"levels"`
			CanDisable bool     `json:"can_disable"`
		} `json:"thinking"`
	} `json:"data"`
	ClientAttestation *struct {
		Token     string `json:"token"`
		ExpiresIn int64  `json:"expires_in"`
	} `json:"client_attestation"`
}

// fetchCatalog loads the model catalog and caches the attestation token it carries.
func fetchCatalog(callbackID string, s Storage) (catalog, error) {
	var raw catalogResponse
	if err := doJSON(callbackID, s, "GET", s.APIBase()+"/v1/models", nil, &raw); err != nil {
		return catalog{}, err
	}

	out := catalog{Models: make([]pluginapi.ModelInfo, 0, len(raw.Data))}
	for _, m := range raw.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		name := m.Name
		if name == "" {
			name = id
		}
		info := pluginapi.ModelInfo{
			ID:                  id,
			Object:              "model",
			OwnedBy:             ProviderName,
			Name:                name,
			DisplayName:         name,
			ContextLength:       m.ContextLength,
			InputTokenLimit:     m.ContextLength,
			MaxCompletionTokens: m.MaxTokens,
			OutputTokenLimit:    m.MaxTokens,
		}
		if m.Vision {
			info.SupportedInputModalities = []string{"text", "image"}
		} else {
			info.SupportedInputModalities = []string{"text"}
		}
		if m.Thinking != nil && len(m.Thinking.Levels) > 0 {
			info.Thinking = &pluginapi.ThinkingSupport{
				Levels:      m.Thinking.Levels,
				ZeroAllowed: m.Thinking.CanDisable,
			}
		}
		out.Models = append(out.Models, info)
	}

	if raw.ClientAttestation != nil {
		out.Attestation = raw.ClientAttestation.Token
		storeAttestation(s.DeviceToken, raw.ClientAttestation.Token, raw.ClientAttestation.ExpiresIn)
	}
	return out, nil
}

// handleModelsForAuth reports the models available to one credential.
//
// Discovery is live: the catalog is the same call that mints the attestation
// token, so there is no separate warm-up request.
func handleModelsForAuth(payload []byte) ([]byte, error) {
	req, err := decode[authModelRequest](payload)
	if err != nil {
		return nil, err
	}
	s, ok := parseStorage(req.StorageJSON)
	if !ok {
		return okEnvelope(pluginapi.ModelResponse{Provider: ProviderName})
	}

	cat, err := fetchCatalog(req.HostCallbackID, s)
	if err != nil {
		// Model discovery must not remove a working credential from rotation on
		// a transient catalog failure; report no models and let CPA retry.
		return okEnvelope(pluginapi.ModelResponse{Provider: ProviderName})
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: ProviderName, Models: cat.Models})
}
