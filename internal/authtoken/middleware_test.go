package authtoken

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const testSecret = "super-secret-key"

func TestIssueAndVerify(t *testing.T) {
	tok, err := Issue("wallet-management", testSecret)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}
	claims, err := verify(tok, testSecret)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Sub != "wallet-management" {
		t.Errorf("expected sub=wallet-management, got %s", claims.Sub)
	}
	if claims.Exp <= claims.Iat {
		t.Errorf("exp (%d) should be > iat (%d)", claims.Exp, claims.Iat)
	}
}

func TestIssueEmptySecret(t *testing.T) {
	if _, err := Issue("svc", ""); err == nil {
		t.Error("expected error issuing with empty secret")
	}
}

func TestVerifyMalformed(t *testing.T) {
	cases := []string{"", "a.b", "a.b.c.d", "not-a-token"}
	for _, tok := range cases {
		if _, err := verify(tok, testSecret); err == nil {
			t.Errorf("expected malformed error for %q", tok)
		}
	}
}

func TestVerifyBadSignature(t *testing.T) {
	tok, _ := Issue("svc", testSecret)
	if _, err := verify(tok, "wrong-secret"); err == nil {
		t.Error("expected invalid signature error")
	}
}

func TestVerifyCorruptPayload(t *testing.T) {
	tok, _ := Issue("svc", testSecret)
	parts := strings.Split(tok, ".")
	parts[1] = encode([]byte("not-json"))
	tok = strings.Join(parts, ".")
	if _, err := verify(tok, testSecret); err == nil {
		t.Error("expected decode/parse error for corrupt payload")
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	in := []byte("hello world \x00 \xff")
	out, err := decode(encode(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("roundtrip mismatch: got %q want %q", out, in)
	}
}

func TestDecodeInvalid(t *testing.T) {
	if _, err := decode("!!!not-base64!!!"); err == nil {
		t.Error("expected decode error for invalid base64")
	}
}

func TestSignMarshallError(t *testing.T) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	claims := Claims{Sub: "x", Iat: 1, Exp: 2}
	if _, err := sign(header, claims, testSecret); err != nil {
		t.Fatalf("sign happy path: %v", err)
	}
}

func TestMiddlewareBypass(t *testing.T) {
	called := false
	h := Middleware(testSecret, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/wallets", nil))
	if !called {
		t.Error("bypass should call handler")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareSkipPaths(t *testing.T) {
	for _, p := range []string{"/healthz", "/readyz", "/metrics"} {
		called := false
		h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if !called {
			t.Errorf("skip path %s should call handler", p)
		}
	}
}

func TestMiddlewareMissingHeader(t *testing.T) {
	called := false
	h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/wallets", nil))
	if called {
		t.Error("handler should not be called with missing header")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	assertUnauthorizedBody(t, rec)
}

func TestMiddlewareMalformedHeader(t *testing.T) {
	h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Basic xyz")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareBadToken(t *testing.T) {
	h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer not-a-valid-token")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMiddlewareExpiredToken(t *testing.T) {
	now := time.Now().UTC()
	claims := Claims{Sub: "svc", Iat: now.Add(-2 * time.Hour).Unix(), Exp: now.Add(-1 * time.Hour).Unix()}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	tok, _ := sign(header, claims, testSecret)
	h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", rec.Code)
	}
}

func TestMiddlewareValidToken(t *testing.T) {
	tok, _ := Issue("svc", testSecret)
	called := false
	h := Middleware(testSecret, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("handler should be called with valid token")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestSecretFromEnvSet(t *testing.T) {
	t.Setenv("SERVICE_TOKEN_SECRET", "abc")
	t.Setenv("DEV_MODE", "")
	s, bypass := SecretFromEnv()
	if s != "abc" || bypass {
		t.Errorf("expected s=abc bypass=false, got s=%q bypass=%v", s, bypass)
	}
}

func TestSecretFromEnvDevMode(t *testing.T) {
	t.Setenv("SERVICE_TOKEN_SECRET", "")
	t.Setenv("DEV_MODE", "1")
	s, bypass := SecretFromEnv()
	if s != "" || !bypass {
		t.Errorf("expected empty secret + bypass=true, got s=%q bypass=%v", s, bypass)
	}
}

func TestSecretFromEnvTestMode(t *testing.T) {
	t.Setenv("SERVICE_TOKEN_SECRET", "")
	t.Setenv("DEV_MODE", "")
	s, bypass := SecretFromEnv()
	if s != "" || !bypass {
		t.Errorf("in test mode expected bypass=true, got s=%q bypass=%v", s, bypass)
	}
}

func TestSecretFromEnvProdFatal(t *testing.T) {
	t.Setenv("SERVICE_TOKEN_SECRET", "")
	t.Setenv("DEV_MODE", "")
	if os.Getenv("DEV_MODE") == "1" {
		t.Skip("DEV_MODE set in env")
	}
	// SecretFromEnv calls log.Fatal when secret unset and not test mode. But
	// testing.Testing() is true during tests, so it returns bypass=true instead.
	// The fatal path cannot be triggered inside a test binary; the dev/test
	// branch is covered by TestSecretFromEnvTestMode above.
	s, bypass := SecretFromEnv()
	if s != "" || !bypass {
		t.Errorf("in test mode expected bypass=true, got s=%q bypass=%v", s, bypass)
	}
}

var _ = json.Marshal

func assertUnauthorizedBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body errBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error body: %v body=%s", err, rec.Body.String())
	}
	if body.Error.Code != "unauthorized" {
		t.Errorf("expected code=unauthorized, got %q", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("expected non-empty error message")
	}
}