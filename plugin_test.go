package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// testStorage builds a credential with a freshly generated device key.
func testStorage(t *testing.T) Storage {
	t.Helper()
	pub, priv, err := generateDeviceKey()
	if err != nil {
		t.Fatalf("generateDeviceKey: %v", err)
	}
	return Storage{
		Type:             ProviderName,
		Email:            "user@example.com",
		DeviceToken:      "u1s1d-test-token",
		DevicePublicJwk:  pub,
		DevicePrivateJwk: priv,
		BaseURL:          DefaultAPIBase + "/v1",
	}
}

func TestAPIBaseStripsVersionSuffix(t *testing.T) {
	cases := map[string]string{
		"":                             DefaultAPIBase,
		"https://api.u1s1.io/v1":       "https://api.u1s1.io",
		"https://api.u1s1.io/v1/":      "https://api.u1s1.io",
		"https://api.u1s1.io":          "https://api.u1s1.io",
		"https://gateway.example.com/": "https://gateway.example.com",
	}
	for in, want := range cases {
		if got := (Storage{BaseURL: in}).APIBase(); got != want {
			t.Errorf("APIBase(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDPoPProofStructure(t *testing.T) {
	s := testStorage(t)
	key, err := privateKey(s)
	if err != nil {
		t.Fatalf("privateKey: %v", err)
	}

	proof, err := dpopProof(s, key, "post", chatURL(s)+"?stream=true#frag")
	if err != nil {
		t.Fatalf("dpopProof: %v", err)
	}
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("proof has %d segments, want 3", len(parts))
	}

	var claims struct {
		Htm string `json:"htm"`
		Htu string `json:"htu"`
		Ath string `json:"ath"`
		Jti string `json:"jti"`
	}
	raw, err := b64urlDecodeForTest(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Htm != "POST" {
		t.Errorf("htm = %q, want POST", claims.Htm)
	}
	if claims.Htu != chatURL(s) {
		t.Errorf("htu = %q, want %q (query and fragment must be stripped)", claims.Htu, chatURL(s))
	}
	if claims.Ath == "" || claims.Jti == "" {
		t.Error("ath and jti must be set")
	}
}

func TestSignedHeadersOmitAcceptEncoding(t *testing.T) {
	s := testStorage(t)
	h, err := signedHeaders(s, http.MethodGet, s.APIBase()+"/v1/me")
	if err != nil {
		t.Fatalf("signedHeaders: %v", err)
	}
	if got := h.Get("accept-encoding"); got != "" {
		t.Errorf("accept-encoding = %q, want empty so the host handles compression", got)
	}
	if !strings.HasPrefix(h.Get("authorization"), "DPoP ") {
		t.Errorf("authorization = %q, want DPoP scheme", h.Get("authorization"))
	}
	if h.Get("dpop") == "" {
		t.Error("dpop header must be set")
	}
}

func TestIsU1S1Record(t *testing.T) {
	valid := testStorage(t)
	if !isU1S1Record(valid) {
		t.Error("typed u1s1 record must be claimed")
	}

	untyped := valid
	untyped.Type = ""
	if !isU1S1Record(untyped) {
		t.Error("record with a u1s1d- device token must be claimed")
	}

	foreign := valid
	foreign.Type = "antigravity"
	foreign.DeviceToken = "ya29.other-provider"
	if isU1S1Record(foreign) {
		t.Error("other providers' auth files must not be claimed")
	}
}

func TestTrimLeadingSpace(t *testing.T) {
	got := trimLeadingSpace([]byte("   \n\n  {\"ok\":true}"))
	if string(got) != `{"ok":true}` {
		t.Errorf("trimLeadingSpace = %q", got)
	}
}

func TestFrameBoundary(t *testing.T) {
	cases := []struct {
		in        string
		wantEnd   int
		wantWidth int
	}{
		{"data: {}\n\n", 8, 2},
		{"data: {}\r\n\r\n", 8, 4},
		{"data: {} incomplete", -1, 0},
		{"a\n\nb\r\n\r\n", 1, 2},
	}
	for _, c := range cases {
		end, width := frameBoundary([]byte(c.in))
		if end != c.wantEnd || width != c.wantWidth {
			t.Errorf("frameBoundary(%q) = (%d,%d), want (%d,%d)", c.in, end, width, c.wantEnd, c.wantWidth)
		}
	}
}

func TestFrameDataExtractsBarePayload(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"data frame", "data: {\"id\":\"x\"}\n\n", `{"id":"x"}`},
		{"event plus data", "event: message\ndata: {\"a\":1}\n\n", `{"a":1}`},
		{"crlf frame", "data: {\"a\":1}\r\n\r\n", `{"a":1}`},
		{"comment keep-alive", ": OPENROUTER PROCESSING\n\n", ""},
		{"multiline data", "data: line1\ndata: line2\n\n", "line1\nline2"},
	}
	for _, c := range cases {
		if got := string(frameData([]byte(c.in))); got != c.want {
			t.Errorf("%s: frameData = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUpstreamErrorPrefersGatewayMessage(t *testing.T) {
	err := upstreamError(401, []byte(`{"error":{"message":"missing or invalid credential"}}`))
	if !strings.Contains(err.Error(), "missing or invalid credential") {
		t.Errorf("error = %q, want the gateway message", err)
	}
	if sc, ok := err.(statusError); !ok || sc.StatusCode() != 401 {
		t.Errorf("error must expose StatusCode 401, got %#v", err)
	}
}

func TestRegistrationDeclaresOAuthScope(t *testing.T) {
	reg := pluginRegistration()
	if reg.Capabilities.ExecutorModelScope != pluginapi.ExecutorModelScopeOAuth {
		t.Errorf("executor scope = %q, want oauth", reg.Capabilities.ExecutorModelScope)
	}
	if reg.Metadata.Name != "u1s1" {
		t.Errorf("plugin name = %q, want u1s1", reg.Metadata.Name)
	}
}

func TestManagementRoutesUsePluginPrefix(t *testing.T) {
	routes := managementRoutes()
	want := map[string]bool{
		"/plugins/u1s1/data":    false,
		"/plugins/u1s1/refresh": false,
		"/plugins/u1s1/import":  false,
	}
	for _, r := range routes.Routes {
		if _, ok := want[r.Path]; !ok {
			t.Errorf("unexpected management route %q", r.Path)
			continue
		}
		want[r.Path] = true
	}
	for path, seen := range want {
		if !seen {
			t.Errorf("missing management route %q", path)
		}
	}
	if len(routes.Resources) != 2 || routes.Resources[0].Path != "/panel" {
		t.Errorf("resources = %#v, want /panel and /data entries", routes.Resources)
	}
}

func TestRemainingPackageTokens(t *testing.T) {
	a := Account{Packages: []Package{{Remaining: 1_999_207}, {Remaining: 2_000_000}}}
	if got := a.RemainingPackageTokens(); got != 3_999_207 {
		t.Errorf("RemainingPackageTokens = %d", got)
	}
}

func TestAuthParseClaimsCLIConfig(t *testing.T) {
	s := testStorage(t)
	s.Type = ""
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal storage: %v", err)
	}
	req, err := json.Marshal(pluginapi.AuthParseRequest{
		FileName: "u1s1-user@example.com.json",
		RawJSON:  raw,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	out, err := handleAuthParse(req)
	if err != nil {
		t.Fatalf("handleAuthParse: %v", err)
	}
	var env struct {
		OK     bool `json:"ok"`
		Result struct {
			Handled bool `json:"Handled"`
			Auth    struct {
				Provider    string `json:"Provider"`
				Label       string `json:"Label"`
				StorageJSON []byte `json:"StorageJSON"`
			} `json:"Auth"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if !env.OK || !env.Result.Handled {
		t.Fatal("a valid u1s1 credential file must be claimed")
	}
	if env.Result.Auth.Provider != ProviderName {
		t.Errorf("provider = %q", env.Result.Auth.Provider)
	}
	if env.Result.Auth.Label != "user@example.com" {
		t.Errorf("label = %q, want the account email", env.Result.Auth.Label)
	}

	stored, ok := parseStorage(env.Result.Auth.StorageJSON)
	if !ok || stored.Type != ProviderName {
		t.Errorf("stored type = %q, want %q", stored.Type, ProviderName)
	}
}

func TestAuthParseIgnoresForeignFiles(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"type":         "antigravity",
		"access_token": "ya29.example",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := json.Marshal(pluginapi.AuthParseRequest{FileName: "antigravity.json", RawJSON: raw})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	out, err := handleAuthParse(req)
	if err != nil {
		t.Fatalf("handleAuthParse: %v", err)
	}
	if strings.Contains(string(out), `"Handled":true`) {
		t.Error("other providers' credentials must be left alone")
	}
}

func TestDispatchRejectsUnknownMethod(t *testing.T) {
	out, err := dispatch("does.not.exist", nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(string(out), "unknown_method") {
		t.Errorf("response = %s, want an unknown_method error envelope", out)
	}
}

func TestDispatchIdentifier(t *testing.T) {
	for _, method := range []string{"auth.identifier", "executor.identifier"} {
		out, err := dispatch(method, nil)
		if err != nil {
			t.Fatalf("dispatch(%s): %v", method, err)
		}
		if !strings.Contains(string(out), `"identifier":"u1s1"`) {
			t.Errorf("dispatch(%s) = %s", method, out)
		}
	}
}

func TestRequestPayloadFallsBackToOriginal(t *testing.T) {
	translated := pluginapi.ExecutorRequest{
		Payload:         []byte(`{"translated":true}`),
		OriginalRequest: []byte(`{"original":true}`),
	}
	if got := string(requestPayload(translated)); got != `{"translated":true}` {
		t.Errorf("payload = %s, want the translated body", got)
	}

	passthrough := pluginapi.ExecutorRequest{OriginalRequest: []byte(`{"original":true}`)}
	if got := string(requestPayload(passthrough)); got != `{"original":true}` {
		t.Errorf("payload = %s, want the original body", got)
	}
}

func TestForwardHeadersDropsHopHeaders(t *testing.T) {
	in := http.Header{
		"Content-Type":      []string{"application/json"},
		"Content-Encoding":  []string{"gzip"},
		"Transfer-Encoding": []string{"chunked"},
		"X-Request-Id":      []string{"abc"},
	}
	out := forwardHeaders(in)
	if out.Get("Content-Encoding") != "" || out.Get("Transfer-Encoding") != "" {
		t.Error("hop-by-hop headers must not be forwarded")
	}
	if out.Get("X-Request-Id") != "abc" {
		t.Error("end-to-end headers must be preserved")
	}
}

func TestRandomStateIsPathSafe(t *testing.T) {
	state := randomState()
	if state == "" || strings.ContainsAny(state, "/\\") {
		t.Errorf("state = %q, must be non-empty and path-safe", state)
	}
}

func b64urlDecodeForTest(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}
