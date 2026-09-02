package main

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// transport centralizes every upstream call so requests are always issued
// through the host HTTP client. Routing through the host keeps CPA's proxy
// configuration, per-credential proxy overrides, and request logging intact.

// doJSON issues an authenticated request and decodes a JSON response body.
func doJSON(callbackID string, s Storage, method, url string, body []byte, out any) error {
	resp, err := do(callbackID, s, method, url, body, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return upstreamError(resp.StatusCode, resp.Body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(trimLeadingSpace(resp.Body), out); err != nil {
		return fmt.Errorf("decode %s response: %w", url, err)
	}
	return nil
}

// do issues an authenticated request through the host HTTP client.
//
// extra headers, when present, override the signed defaults; this is how
// content-type and SSE accept headers are applied.
func do(callbackID string, s Storage, method, url string, body []byte, extra http.Header) (pluginapi.HTTPResponse, error) {
	headers, err := signedHeaders(s, method, url)
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}
	for k, v := range extra {
		headers[http.CanonicalHeaderKey(k)] = v
	}

	raw, err := hostCall(methodHostHTTPDo, hostHTTPRequest{
		HTTPRequest: pluginapi.HTTPRequest{
			Method:  method,
			URL:     url,
			Headers: headers,
			Body:    body,
		},
		HostCallbackID: callbackID,
	})
	if err != nil {
		return pluginapi.HTTPResponse{}, err
	}

	var resp pluginapi.HTTPResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HTTPResponse{}, fmt.Errorf("decode host http response: %w", err)
	}
	return resp, nil
}

// doStream opens a streaming upstream request and returns the host stream ID.
func doStream(callbackID string, s Storage, method, url string, body []byte, extra http.Header) (hostHTTPStreamResponse, error) {
	headers, err := signedHeaders(s, method, url)
	if err != nil {
		return hostHTTPStreamResponse{}, err
	}
	for k, v := range extra {
		headers[http.CanonicalHeaderKey(k)] = v
	}

	raw, err := hostCall(methodHostHTTPDoStream, hostHTTPRequest{
		HTTPRequest: pluginapi.HTTPRequest{
			Method:  method,
			URL:     url,
			Headers: headers,
			Body:    body,
		},
		HostCallbackID: callbackID,
	})
	if err != nil {
		return hostHTTPStreamResponse{}, err
	}

	var resp hostHTTPStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("decode host stream response: %w", err)
	}
	return resp, nil
}

// upstreamError renders an upstream failure, preferring the gateway's own
// error message over the raw body.
func upstreamError(status int, body []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(trimLeadingSpace(body), &payload); err == nil {
		if msg := strings.TrimSpace(payload.Error.Message); msg != "" {
			return statusError{code: status, msg: msg}
		}
	}
	snippet := strings.TrimSpace(string(body))
	if len(snippet) > 512 {
		snippet = snippet[:512]
	}
	return statusError{code: status, msg: snippet}
}

// statusError carries an HTTP status so CPA can propagate it downstream.
type statusError struct {
	code int
	msg  string
}

func (e statusError) Error() string {
	if e.msg == "" {
		return fmt.Sprintf("upstream returned %d", e.code)
	}
	return fmt.Sprintf("upstream %d: %s", e.code, e.msg)
}

// StatusCode lets the host map plugin errors onto HTTP responses.
func (e statusError) StatusCode() int { return e.code }

// trimLeadingSpace strips the leading whitespace the u1s1 gateway emits ahead
// of non-streaming JSON bodies as a connection keep-alive.
func trimLeadingSpace(b []byte) []byte {
	return []byte(strings.TrimLeft(string(b), " \r\n\t"))
}

// --- attestation ---

// The gateway issues a short-lived, device-bound attestation token from
// /v1/models and expects it echoed on inference calls. It is cached per
// credential and refreshed shortly before expiry.

type attestation struct {
	token   string
	expires time.Time
}

var (
	attestationMu    sync.RWMutex
	attestationCache = map[string]attestation{}
)

// attestationToken returns a valid attestation token, refreshing when needed.
// A missing token is not fatal: the gateway currently accepts requests without
// it, so callers proceed with an empty value rather than failing the request.
func attestationToken(callbackID string, s Storage) string {
	attestationMu.RLock()
	cached, ok := attestationCache[s.DeviceToken]
	attestationMu.RUnlock()
	if ok && time.Now().Before(cached.expires.Add(-time.Minute)) {
		return cached.token
	}

	catalog, err := fetchCatalog(callbackID, s)
	if err != nil {
		return ""
	}
	return catalog.Attestation
}

// storeAttestation caches an attestation token returned by the model catalog.
func storeAttestation(deviceToken, token string, expiresIn int64) {
	if token == "" {
		return
	}
	if expiresIn <= 0 {
		expiresIn = 604800 // gateway default: 7 days
	}
	attestationMu.Lock()
	attestationCache[deviceToken] = attestation{
		token:   token,
		expires: time.Now().Add(time.Duration(expiresIn) * time.Second),
	}
	attestationMu.Unlock()
}

// resetCaches clears process-wide state on plugin shutdown.
func resetCaches() {
	attestationMu.Lock()
	attestationCache = map[string]attestation{}
	attestationMu.Unlock()

	keyCacheMu.Lock()
	keyCache = map[string]*ecdsa.PrivateKey{}
	keyCacheMu.Unlock()

	quotaMu.Lock()
	quotaCache = map[string]quotaEntry{}
	quotaMu.Unlock()
}
