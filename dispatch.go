package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// dispatch routes one ABI method call to its handler.
func dispatch(method string, payload []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(pluginRegistration())

	// The auth, executor, and model provider share one provider key.
	case pluginabi.MethodAuthIdentifier, pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: ProviderName})

	case pluginabi.MethodAuthParse:
		return handleAuthParse(payload)
	case pluginabi.MethodAuthLoginStart:
		return handleLoginStart(payload)
	case pluginabi.MethodAuthLoginPoll:
		return handleLoginPoll(payload)
	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(payload)

	case pluginabi.MethodModelForAuth:
		return handleModelsForAuth(payload)

	case pluginabi.MethodExecutorExecute:
		return handleExecute(payload)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecuteStream(payload)
	case pluginabi.MethodExecutorCountTokens:
		return handleCountTokens(payload)
	case pluginabi.MethodExecutorHTTPRequest:
		return handleHTTPRequest(payload)

	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRoutes())
	case pluginabi.MethodManagementHandle:
		return handleManagement(payload)

	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

// pluginRegistration declares the plugin's capabilities to CPA.
//
// ExecutorModelScopeOAuth is required: models are discovered per credential, so
// the executor must not be offered for static model configuration.
func pluginRegistration() registration {
	return registration{
		SchemaVersion: 1,
		Metadata: metadata{
			Name:             "u1s1",
			Version:          PluginVersion,
			Author:           "Lei-rr",
			GitHubRepository: "https://github.com/Lei-rr/u1s1-cpa",
			ConfigFields:     []configField{},
		},
		Capabilities: capabilities{
			AuthProvider:       true,
			ModelProvider:      true,
			Executor:           true,
			ExecutorModelScope: pluginapi.ExecutorModelScopeOAuth,
			// u1s1 speaks OpenAI Chat Completions, so no translation is needed
			// in either direction. CPA still bridges other client protocols.
			ExecutorInputFormats:  []string{"openai"},
			ExecutorOutputFormats: []string{"openai"},
			ManagementAPI:         true,
		},
	}
}

// managementRoutes declares the panel and its data endpoints.
//
// The authenticated /v0/management routes carry account data. The resource
// route serves the panel shell, which CPA exposes without management auth.
func managementRoutes() managementRegistration {
	base := "/plugins/" + PluginID
	return managementRegistration{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: base + "/data", Description: "u1s1 balance, packages, and claim state"},
			{Method: http.MethodPost, Path: base + "/refresh", Description: "Invalidate the cache and re-read account state"},
			{Method: http.MethodPost, Path: base + "/import", Description: "Import a u1s1 CLI config.json as a credential"},
		},
		Resources: []managementResource{
			{Path: "/panel", Menu: "u1s1", Description: "u1s1 额度与用量面板"},
			{Path: "/data", Menu: "", Description: "u1s1 数据接口"},
		},
	}
}

// jsonBody renders a JSON management response.
func jsonBody(status int, body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal response: %w", err)
	}
	return okEnvelope(managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
		Body:       raw,
	})
}

// jsonError renders a JSON error management response.
func jsonError(status int, message string) ([]byte, error) {
	return jsonBody(status, map[string]string{"error": message})
}

// htmlBody renders an HTML management response.
func htmlBody(status int, body string) ([]byte, error) {
	return okEnvelope(managementResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:       []byte(body),
	})
}
