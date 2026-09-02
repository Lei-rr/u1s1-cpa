package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Credential lifecycle: u1s1 device tokens are long-lived and sender-constrained
// by the device key, so there is no refresh token to rotate. ParseAuth adopts an
// existing CLI credential file, and login runs the browser device-approval flow.

// handleAuthParse claims u1s1 auth files in the CPA auth directory.
func handleAuthParse(payload []byte) ([]byte, error) {
	req, err := decode[pluginapi.AuthParseRequest](payload)
	if err != nil {
		return nil, err
	}

	var s Storage
	if err := json.Unmarshal(req.RawJSON, &s); err != nil {
		return okEnvelope(pluginapi.AuthParseResponse{})
	}
	if !isU1S1Record(s) {
		return okEnvelope(pluginapi.AuthParseResponse{})
	}
	s.Type = ProviderName

	storage, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}

	id := req.FileName
	if id == "" {
		id = s.FileName()
	}
	return okEnvelope(pluginapi.AuthParseResponse{
		Handled: true,
		Auth: pluginapi.AuthData{
			Provider:         ProviderName,
			ID:               id,
			FileName:         req.FileName,
			Label:            s.Label(),
			Prefix:           s.Prefix,
			ProxyURL:         s.ProxyURL,
			Disabled:         s.Disabled,
			StorageJSON:      storage,
			Metadata:         map[string]any{"type": ProviderName, "email": s.Email},
			Attributes:       map[string]string{"type": ProviderName},
			NextRefreshAfter: time.Now().Add(refreshInterval),
		},
	})
}

// handleAuthRefresh revalidates a credential.
//
// There is nothing to rotate, so this confirms the device token still works by
// reading the account endpoint. A live check lets CPA mark dead credentials
// instead of routing traffic to them.
func handleAuthRefresh(payload []byte) ([]byte, error) {
	req, err := decode[authRefreshRequest](payload)
	if err != nil {
		return nil, err
	}
	s, ok := parseStorage(req.StorageJSON)
	if !ok {
		return nil, fmt.Errorf("u1s1 credential is missing a device key")
	}

	account, err := fetchAccount(req.HostCallbackID, s)
	if err != nil {
		return nil, err
	}

	// Adopt the server-reported email when the local file predates it.
	storage := req.StorageJSON
	if s.Email == "" && account.Email != "" {
		s.Email = account.Email
		if updated, errMarshal := json.Marshal(s); errMarshal == nil {
			storage = updated
		}
	}

	next := time.Now().Add(refreshInterval)
	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth: pluginapi.AuthData{
			Provider:         ProviderName,
			ID:               req.AuthID,
			Label:            s.Label(),
			StorageJSON:      storage,
			Metadata:         req.Metadata,
			Attributes:       req.Attributes,
			NextRefreshAfter: next,
		},
		NextRefreshAfter: next,
	})
}

// deviceFlow is the login state carried across StartLogin and PollLogin.
//
// CPA persists it in the OAuth session metadata and hands it back on each poll,
// so the plugin keeps no login state of its own.
type deviceFlow struct {
	PollSecret string          `json:"poll_secret"`
	PublicJwk  json.RawMessage `json:"public_jwk"`
	PrivateJwk json.RawMessage `json:"private_jwk"`
	APIBase    string          `json:"api_base"`
}

// handleLoginStart begins u1s1 device authorization.
//
// This is a device-approval flow, not an OAuth redirect: the returned URL is a
// u1s1 login page the user approves in the browser, and no callback reaches CPA.
// Completion is detected by polling, which the management UI drives.
func handleLoginStart(payload []byte) ([]byte, error) {
	req, err := decode[authLoginStartRequest](payload)
	if err != nil {
		return nil, err
	}

	pub, priv, err := generateDeviceKey()
	if err != nil {
		return nil, err
	}

	var pubObj any
	if err := json.Unmarshal(pub, &pubObj); err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]any{
		"public_jwk":     pubObj,
		"device_name":    "CLIProxyAPI",
		"client_version": clientVersion,
	})
	if err != nil {
		return nil, err
	}

	// The device-start call is unauthenticated: no credential exists yet, so it
	// cannot go through the signed transport helpers.
	var start struct {
		VerifyURL  string `json:"verify_url"`
		PollSecret string `json:"poll_secret"`
		ExpiresIn  int    `json:"expires_in"`
	}
	if err := postPublic(req.HostCallbackID, DefaultWebBase+"/auth/device/start", body, &start); err != nil {
		return nil, err
	}
	if start.VerifyURL == "" || start.PollSecret == "" {
		return nil, fmt.Errorf("u1s1 device start returned an incomplete response")
	}

	flow, err := json.Marshal(deviceFlow{
		PollSecret: start.PollSecret,
		PublicJwk:  pub,
		PrivateJwk: priv,
		APIBase:    DefaultAPIBase,
	})
	if err != nil {
		return nil, err
	}

	expires := start.ExpiresIn
	if expires <= 0 {
		expires = 900
	}
	return okEnvelope(pluginapi.AuthLoginStartResponse{
		Provider:  ProviderName,
		URL:       start.VerifyURL,
		State:     randomState(),
		ExpiresAt: time.Now().Add(time.Duration(expires) * time.Second),
		Metadata:  map[string]any{"flow": string(flow)},
	})
}

// handleLoginPoll checks whether the browser approval completed.
func handleLoginPoll(payload []byte) ([]byte, error) {
	req, err := decode[authLoginPollRequest](payload)
	if err != nil {
		return nil, err
	}

	raw, _ := req.Metadata["flow"].(string)
	var flow deviceFlow
	if err := json.Unmarshal([]byte(raw), &flow); err != nil || flow.PollSecret == "" {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: "login session state is missing; restart the login",
		})
	}

	body, err := json.Marshal(map[string]string{"poll_secret": flow.PollSecret})
	if err != nil {
		return nil, err
	}
	var poll struct {
		Status      string      `json:"status"`
		APIKey      string      `json:"api_key"`
		DeviceToken string      `json:"device_token"`
		DeviceID    json.Number `json:"device_id"`
		Error       string      `json:"error"`
	}
	if err := postPublic(req.HostCallbackID, DefaultWebBase+"/auth/device/poll", body, &poll); err != nil {
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for browser approval",
		})
	}

	switch {
	case poll.Error != "":
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusError,
			Message: poll.Error,
		})
	case poll.DeviceToken == "":
		return okEnvelope(pluginapi.AuthLoginPollResponse{
			Status:  pluginapi.AuthLoginStatusPending,
			Message: "waiting for browser approval",
		})
	}

	s := Storage{
		Type:             ProviderName,
		APIKey:           poll.APIKey,
		DeviceToken:      poll.DeviceToken,
		DeviceID:         poll.DeviceID,
		DevicePublicJwk:  flow.PublicJwk,
		DevicePrivateJwk: flow.PrivateJwk,
		BaseURL:          DefaultAPIBase,
	}
	if account, errAccount := fetchAccount(req.HostCallbackID, s); errAccount == nil {
		s.Email = account.Email
	}

	storage, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.AuthLoginPollResponse{
		Status:  pluginapi.AuthLoginStatusSuccess,
		Message: "u1s1 login complete",
		Auth: pluginapi.AuthData{
			Provider:         ProviderName,
			ID:               s.FileName(),
			FileName:         s.FileName(),
			Label:            s.Label(),
			StorageJSON:      storage,
			Metadata:         map[string]any{"type": ProviderName, "email": s.Email},
			Attributes:       map[string]string{"type": ProviderName},
			NextRefreshAfter: time.Now().Add(refreshInterval),
		},
	})
}

// postPublic issues an unauthenticated JSON POST through the host client.
func postPublic(callbackID, url string, body []byte, out any) error {
	headers := http.Header{}
	headers.Set("content-type", "application/json")
	headers.Set("accept", "application/json")
	headers.Set("user-agent", userAgent)

	raw, err := hostCall(methodHostHTTPDo, hostHTTPRequest{
		HTTPRequest: pluginapi.HTTPRequest{
			Method:  http.MethodPost,
			URL:     url,
			Headers: headers,
			Body:    body,
		},
		HostCallbackID: callbackID,
	})
	if err != nil {
		return err
	}

	var resp pluginapi.HTTPResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamError(resp.StatusCode, resp.Body)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(trimLeadingSpace(resp.Body), out)
}

// randomState returns an OAuth session state token.
//
// CPA rejects states containing path separators, so the hex form of a UUID is
// used rather than raw base64.
func randomState() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
