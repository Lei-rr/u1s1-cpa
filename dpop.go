package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// u1s1 authenticates every gateway call with a DPoP proof (RFC 9449) signed by
// the device key generated at login. The proof binds the HTTP method and URL,
// so a fresh JWT is required per request.

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// jwk is the subset of the JSON Web Key fields u1s1 uses (EC P-256).
type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d,omitempty"`
}

// keyCache avoids re-deriving the ECDSA key for every request. Parsing a JWK
// costs a big.Int decode plus curve setup; requests are hot paths.
var (
	keyCacheMu sync.RWMutex
	keyCache   = map[string]*ecdsa.PrivateKey{}
)

// privateKey parses and caches the ECDSA P-256 private key for a credential.
func privateKey(s Storage) (*ecdsa.PrivateKey, error) {
	cacheKey := s.DeviceToken
	keyCacheMu.RLock()
	if key := keyCache[cacheKey]; key != nil {
		keyCacheMu.RUnlock()
		return key, nil
	}
	keyCacheMu.RUnlock()

	key, err := parsePrivateJWK(s.DevicePrivateJwk)
	if err != nil {
		return nil, err
	}

	keyCacheMu.Lock()
	keyCache[cacheKey] = key
	keyCacheMu.Unlock()
	return key, nil
}

// parsePrivateJWK converts an EC P-256 private JWK into an ecdsa.PrivateKey.
func parsePrivateJWK(raw json.RawMessage) (*ecdsa.PrivateKey, error) {
	var k jwk
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("decode private jwk: %w", err)
	}
	if k.Kty != "EC" || (k.Crv != "" && k.Crv != "P-256") {
		return nil, fmt.Errorf("unsupported jwk %s/%s, want EC/P-256", k.Kty, k.Crv)
	}
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("decode jwk x: %w", err)
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("decode jwk y: %w", err)
	}
	d, err := base64.RawURLEncoding.DecodeString(k.D)
	if err != nil {
		return nil, fmt.Errorf("decode jwk d: %w", err)
	}
	return &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(x),
			Y:     new(big.Int).SetBytes(y),
		},
		D: new(big.Int).SetBytes(d),
	}, nil
}

// generateDeviceKey creates a new device key pair and returns its JWK encodings.
func generateDeviceKey() (publicJWK, privateJWK json.RawMessage, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate device key: %w", err)
	}
	// P-256 coordinates must be left-padded to 32 bytes.
	x := b64url(key.PublicKey.X.FillBytes(make([]byte, 32)))
	y := b64url(key.PublicKey.Y.FillBytes(make([]byte, 32)))

	pub, err := json.Marshal(jwk{Kty: "EC", Crv: "P-256", X: x, Y: y})
	if err != nil {
		return nil, nil, err
	}
	priv, err := json.Marshal(jwk{
		Kty: "EC", Crv: "P-256", X: x, Y: y,
		D: b64url(key.D.FillBytes(make([]byte, 32))),
	})
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

// dpopProof builds the DPoP JWT for one request.
//
// htu must exclude the query and fragment, and ath is the base64url SHA-256 of
// the access token, per the u1s1 gateway's DPoP validation.
func dpopProof(s Storage, key *ecdsa.PrivateKey, method, rawURL string) (string, error) {
	var pub any
	if len(s.DevicePublicJwk) > 0 {
		if err := json.Unmarshal(s.DevicePublicJwk, &pub); err != nil {
			return "", fmt.Errorf("decode public jwk: %w", err)
		}
	}
	if pub == nil {
		pub = jwk{
			Kty: "EC", Crv: "P-256",
			X: b64url(key.PublicKey.X.FillBytes(make([]byte, 32))),
			Y: b64url(key.PublicKey.Y.FillBytes(make([]byte, 32))),
		}
	}

	header, err := json.Marshal(map[string]any{"typ": "dpop+jwt", "alg": "ES256", "jwk": pub})
	if err != nil {
		return "", err
	}

	ath := sha256.Sum256([]byte(s.DeviceToken))
	htu := rawURL
	if i := strings.IndexAny(htu, "?#"); i >= 0 {
		htu = htu[:i]
	}
	payload, err := json.Marshal(map[string]any{
		"jti": strings.ReplaceAll(uuid.NewString(), "-", ""),
		"htm": strings.ToUpper(method),
		"htu": htu,
		"iat": time.Now().Unix(),
		"ath": b64url(ath[:]),
	})
	if err != nil {
		return "", err
	}

	signingInput := b64url(header) + "." + b64url(payload)
	digest := sha256.Sum256([]byte(signingInput))
	r, sig, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign dpop: %w", err)
	}
	// ES256 signatures are the raw 32-byte r and s values concatenated.
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])

	return signingInput + "." + b64url(raw), nil
}

// signedHeaders returns the full client header set for an authenticated request.
//
// The header set mirrors the official CLI's fingerprint. Accept-Encoding is
// deliberately omitted: the host HTTP client negotiates and transparently
// decodes compression, and advertising gzip here would surface compressed bytes
// to the caller.
func signedHeaders(s Storage, method, rawURL string) (http.Header, error) {
	key, err := privateKey(s)
	if err != nil {
		return nil, err
	}
	proof, err := dpopProof(s, key, method, rawURL)
	if err != nil {
		return nil, err
	}

	h := http.Header{}
	h.Set("accept", "application/json")
	h.Set("user-agent", userAgent)
	h.Set("x-u1s1-client", "terminal")
	h.Set("x-u1s1-version", clientVersion)
	h.Set("x-u1s1-platform", "linux-x64")
	h.Set("authorization", "DPoP "+s.DeviceToken)
	h.Set("dpop", proof)
	return h, nil
}
