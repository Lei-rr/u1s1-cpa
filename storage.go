// Package main implements the u1s1 provider plugin for CLIProxyAPI.
//
// The plugin proxies u1s1 (api.u1s1.io) as a native CPA provider using the
// device-bound DPoP credential stored by the official u1s1 CLI, and exposes a
// Management API panel that reports balance, usage packages, and claimable
// rewards.
package main

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	// PluginID must match the dynamic library file name; CPA derives the plugin ID from it.
	PluginID = "u1s1"
	// PluginVersion is reported to the CPA plugin registry.
	PluginVersion = "0.2.6"
	// ProviderName is the CPA provider key owned by this plugin.
	ProviderName = "u1s1"

	// DefaultAPIBase is the u1s1 inference gateway.
	DefaultAPIBase = "https://api.u1s1.io"
	// DefaultWebBase is the u1s1 site that owns the reward/check-in endpoints.
	DefaultWebBase = "https://u1s1.io"

	clientVersion = "1.4.0"
	userAgent     = "pi (linux 7.0.0-1011-aws; x64)"
)

// Storage is the provider-owned auth record persisted by CPA.
//
// It is a superset of the official ~/.u1s1/config.json shape so an existing CLI
// credential file can be copied into the CPA auth directory unchanged.
type Storage struct {
	Type             string          `json:"type"`
	Email            string          `json:"email,omitempty"`
	APIKey           string          `json:"apiKey,omitempty"`
	DeviceToken      string          `json:"deviceToken"`
	DeviceID         json.Number     `json:"deviceId,omitempty"`
	DevicePublicJwk  json.RawMessage `json:"devicePublicJwk"`
	DevicePrivateJwk json.RawMessage `json:"devicePrivateJwk"`
	BaseURL          string          `json:"baseUrl,omitempty"`

	// Optional CPA-side fields honoured when present in the auth file.
	Disabled bool   `json:"disabled,omitempty"`
	ProxyURL string `json:"proxy_url,omitempty"`
	Prefix   string `json:"prefix,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Valid reports whether the record carries a usable device credential.
func (s Storage) Valid() bool {
	return strings.TrimSpace(s.DeviceToken) != "" &&
		len(s.DevicePrivateJwk) > 0 &&
		len(s.DevicePublicJwk) > 0
}

// APIBase returns the normalized inference base URL without a trailing /v1.
//
// The official CLI stores baseUrl as "https://api.u1s1.io/v1"; every request
// path in this plugin is built from the origin, so the suffix is stripped once
// here instead of at each call site.
func (s Storage) APIBase() string {
	base := strings.TrimSpace(s.BaseURL)
	if base == "" {
		return DefaultAPIBase
	}
	base = strings.TrimRight(base, "/")
	return strings.TrimSuffix(base, "/v1")
}

// WebBase returns the site origin that serves reward and check-in endpoints.
func (s Storage) WebBase() string {
	base := s.APIBase()
	if base == DefaultAPIBase {
		return DefaultWebBase
	}
	// Custom deployments are assumed to co-locate the site and the API.
	return base
}

// Label returns a stable display name for the credential.
func (s Storage) Label() string {
	if email := strings.TrimSpace(s.Email); email != "" {
		return email
	}
	if id := strings.TrimSpace(s.DeviceID.String()); id != "" {
		return "u1s1-" + id
	}
	return "u1s1-account"
}

// FileName returns the auth file name CPA should persist this record under.
func (s Storage) FileName() string {
	return "u1s1-" + s.Label() + ".json"
}

// parseStorage decodes a provider auth record.
func parseStorage(raw []byte) (Storage, bool) {
	var s Storage
	if len(raw) == 0 {
		return s, false
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, false
	}
	return s, s.Valid()
}

// isU1S1Record reports whether an auth file belongs to this provider.
//
// A file is claimed when it declares type "u1s1" or when it structurally
// matches a u1s1 device credential, which lets unmodified CLI config files be
// dropped into the auth directory.
func isU1S1Record(s Storage) bool {
	if strings.EqualFold(strings.TrimSpace(s.Type), ProviderName) {
		return true
	}
	return s.Valid() && strings.HasPrefix(s.DeviceToken, "u1s1d-")
}

// refreshInterval is how often CPA is asked to revalidate a credential.
const refreshInterval = 12 * time.Hour
