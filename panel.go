package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// AccountView is one credential as rendered by the panel.
type AccountView struct {
	AuthIndex string `json:"auth_index"`
	ID        string `json:"id"`
	Label     string `json:"label"`
	Email     string `json:"email"`
	Disabled  bool   `json:"disabled"`
	Status    string `json:"status"`
	Success   int64  `json:"success"`
	Failed    int64  `json:"failed"`

	Account *Account `json:"account,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// PanelData is the payload consumed by the panel front end.
type PanelData struct {
	Version      string        `json:"version"`
	UpdatedAt    string        `json:"updated_at"`
	DashboardURL string        `json:"dashboard_url"`
	TotalUSD     float64       `json:"total_usd"`
	TotalTokens  int64         `json:"total_tokens"`
	TotalSuccess int64         `json:"total_success"`
	TotalFailed  int64         `json:"total_failed"`
	Accounts     []AccountView `json:"accounts"`
}

// handleManagement routes a management or resource request.
func handleManagement(payload []byte) ([]byte, error) {
	req, err := decode[managementRequest](payload)
	if err != nil {
		return nil, err
	}

	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}

	path := strings.TrimRight(strings.TrimSpace(req.Path), "/")
	path = strings.TrimPrefix(path, "/v0/management/plugins/"+PluginID)
	path = strings.TrimPrefix(path, "/v0/resource/plugins/"+PluginID)
	if path == "" {
		path = "/panel"
	}

	switch path {
	case "/panel":
		return htmlBody(http.StatusOK, panelHTML)
	case "/data":
		return jsonBody(http.StatusOK, collectPanel(req.HostCallbackID))
	case "/refresh":
		if method != http.MethodPost {
			return jsonError(http.StatusMethodNotAllowed, "POST required")
		}
		invalidateAccounts()
		return jsonBody(http.StatusOK, collectPanel(req.HostCallbackID))
	case "/import":
		if method != http.MethodPost {
			return jsonError(http.StatusMethodNotAllowed, "POST required")
		}
		return handleImport(req)
	default:
		return jsonError(http.StatusNotFound, "route not found")
	}
}

// collectPanel builds the panel snapshot from all u1s1 credentials.
func collectPanel(callbackID string) PanelData {
	data := PanelData{
		Version:      PluginVersion,
		UpdatedAt:    time.Now().Format(time.RFC3339),
		DashboardURL: DefaultWebBase + "/dashboard",
		Accounts:     []AccountView{},
	}

	raw, err := hostCall(methodHostAuthList, struct{}{})
	if err != nil {
		return data
	}
	var list hostAuthListResponse
	if err := json.Unmarshal(raw, &list); err != nil {
		return data
	}

	for _, file := range list.Files {
		if !isOwnedCredential(file) {
			continue
		}
		view := AccountView{
			AuthIndex: file.AuthIndex,
			ID:        file.ID,
			Label:     file.Label,
			Email:     file.Email,
			Disabled:  file.Disabled,
			Status:    file.Status,
			Success:   file.Success,
			Failed:    file.Failed,
		}
		data.TotalSuccess += file.Success
		data.TotalFailed += file.Failed

		s, ok := loadCredential(file.AuthIndex)
		if !ok {
			view.Error = "无法读取凭证内容"
			data.Accounts = append(data.Accounts, view)
			continue
		}
		if view.Email == "" {
			view.Email = s.Email
		}
		if view.Label == "" {
			view.Label = s.Label()
		}

		account, err := fetchAccount(callbackID, s)
		if err != nil {
			view.Error = err.Error()
			data.Accounts = append(data.Accounts, view)
			continue
		}

		view.Account = &account
		data.TotalUSD += account.RemainingUSD
		data.TotalTokens += account.RemainingPackageTokens()
		data.Accounts = append(data.Accounts, view)
	}
	return data
}

// isOwnedCredential reports whether a host credential belongs to this provider.
func isOwnedCredential(file pluginapi.HostAuthFileEntry) bool {
	return strings.EqualFold(strings.TrimSpace(file.Provider), ProviderName) ||
		strings.EqualFold(strings.TrimSpace(file.Type), ProviderName)
}

// loadCredential reads one credential's storage JSON from the host.
func loadCredential(authIndex string) (Storage, bool) {
	raw, err := hostCall(methodHostAuthGet, hostAuthGetRequest{AuthIndex: authIndex})
	if err != nil {
		return Storage{}, false
	}
	var resp hostAuthGetResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return Storage{}, false
	}
	return parseStorage(resp.JSON)
}

// handleImport persists a u1s1 CLI config.json as a CPA credential.
func handleImport(req managementRequest) ([]byte, error) {
	if len(req.Body) == 0 {
		return jsonError(http.StatusBadRequest, "请求体为空")
	}

	var s Storage
	if err := json.Unmarshal(req.Body, &s); err != nil {
		return jsonError(http.StatusBadRequest, "JSON 解析失败: "+err.Error())
	}
	if !s.Valid() {
		return jsonError(http.StatusBadRequest, "缺少 deviceToken 或设备密钥（devicePublicJwk / devicePrivateJwk）")
	}
	s.Type = ProviderName
	if s.BaseURL == "" {
		s.BaseURL = DefaultAPIBase
	}

	account, err := fetchAccount(req.HostCallbackID, s)
	if err != nil {
		return jsonError(http.StatusBadGateway, "凭证校验失败: "+err.Error())
	}
	if account.Email != "" {
		s.Email = account.Email
	}

	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	raw, err := hostCall(methodHostAuthSave, hostAuthSaveRequest{
		Name: s.FileName(),
		JSON: body,
	})
	if err != nil {
		return jsonError(http.StatusInternalServerError, "保存失败: "+err.Error())
	}

	var saved hostAuthSaveResponse
	_ = json.Unmarshal(raw, &saved)
	return jsonBody(http.StatusOK, map[string]any{
		"ok":    true,
		"file":  saved.Name,
		"email": s.Email,
	})
}
