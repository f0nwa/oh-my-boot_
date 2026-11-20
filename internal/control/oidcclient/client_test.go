package oidcclient

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func signTestRS256(t *testing.T, priv *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	hdr, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid, "typ": "JWT"})
	pay, _ := json.Marshal(claims)
	h64 := base64.RawURLEncoding.EncodeToString(hdr)
	p64 := base64.RawURLEncoding.EncodeToString(pay)
	input := h64 + "." + p64
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestOIDCClientFullFlowAndBackChannelLogout(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwks := JWKS{
		Keys: []JWK{{
			Kty: "RSA",
			Kid: "k1",
			Use: "sig",
			Alg: "RS256",
			N:   base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
			E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
		}},
	}

	var srv *httptest.Server
	userEnabled := true
	userRoles := []string{"operator", "observer"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /realms/corp/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := srv.URL + "/realms/corp"
		_ = json.NewEncoder(w).Encode(Discovery{
			Issuer:                base,
			AuthorizationEndpoint: base + "/protocol/openid-connect/auth",
			TokenEndpoint:         base + "/protocol/openid-connect/token",
			JWKSURI:               base + "/protocol/openid-connect/certs",
			EndSessionEndpoint:    base + "/protocol/openid-connect/logout",
		})
	})
	mux.HandleFunc("GET /realms/corp/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jwks)
	})
	mux.HandleFunc("POST /realms/corp/protocol/openid-connect/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.FormValue("grant_type") {
		case "authorization_code":
			idTok := signTestRS256(t, priv, "k1", map[string]any{
				"iss":                srv.URL + "/realms/corp",
				"sub":                "user-sub-1",
				"aud":                "omb-control",
				"exp":                time.Now().Add(5 * time.Minute).Unix(),
				"nonce":              "nonce-123",
				"sid":                "sid-999",
				"preferred_username": "m.sidorova",
				"name":               "Maria Sidorova",
			})
			_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idTok})
		case "client_credentials":
			if r.FormValue("client_id") != "omb-reader" || r.FormValue("client_secret") != "svc-sec" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "svc-tok"})
		}
	})
	mux.HandleFunc("GET /admin/realms/corp/clients", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]string{{"id": "uuid-omb", "clientId": "omb-control"}})
	})
	mux.HandleFunc("GET /admin/realms/corp/users/user-sub-1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"enabled":   userEnabled,
			"username":  "m.sidorova",
			"firstName": "Maria",
			"lastName":  "Sidorova",
		})
	})
	mux.HandleFunc("GET /admin/realms/corp/users/user-sub-1/role-mappings/clients/uuid-omb/composite", func(w http.ResponseWriter, r *http.Request) {
		out := make([]map[string]string, 0, len(userRoles))
		for _, role := range userRoles {
			out = append(out, map[string]string{"name": role})
		}
		_ = json.NewEncoder(w).Encode(out)
	})

	srv = httptest.NewTLSServer(mux)
	defer srv.Close()

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	cl, err := New(Config{
		PublicBaseURL:       "https://pxe.corp.local:8443",
		IssuerURL:           srv.URL + "/realms/corp",
		ClientID:            "omb-control",
		ClientSecret:        "cl-sec",
		ServiceClientID:     "omb-reader",
		ServiceClientSecret: "svc-sec",
		CAPEM:               caPEM,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	doc, err := cl.Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	fetchedJWKS, err := cl.FetchJWKS(ctx, doc.JWKSURI)
	if err != nil {
		t.Fatalf("FetchJWKS: %v", err)
	}
	idTok, err := cl.ExchangeCode(ctx, doc, "code-1", "verifier-1")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	claims, err := cl.ValidateIDToken(idTok, fetchedJWKS, "nonce-123", time.Now())
	if err != nil {
		t.Fatalf("ValidateIDToken: %v", err)
	}
	if claims.Sub != "user-sub-1" || claims.SID != "sid-999" || claims.PreferredUsername != "m.sidorova" {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if _, err := cl.ValidateIDToken(idTok, fetchedJWKS, "wrong-nonce", time.Now()); err == nil {
		t.Fatal("expected nonce mismatch error")
	}

	st, err := cl.FetchUserStatus(ctx, doc, "user-sub-1")
	if err != nil {
		t.Fatalf("FetchUserStatus: %v", err)
	}
	if !st.Enabled || len(st.Roles) != 2 {
		t.Fatalf("unexpected user status: %+v", st)
	}

	logoutTok := signTestRS256(t, priv, "k1", map[string]any{
		"iss":    srv.URL + "/realms/corp",
		"aud":    "omb-control",
		"sub":    "user-sub-1",
		"sid":    "sid-999",
		"jti":    "jti-unique-1",
		"exp":    time.Now().Add(5 * time.Minute).Unix(),
		"events": map[string]any{backChannelLogoutEvent: map[string]any{}},
	})
	lc, err := cl.ValidateLogoutToken(logoutTok, fetchedJWKS, time.Now())
	if err != nil {
		t.Fatalf("ValidateLogoutToken: %v", err)
	}
	if lc.SID != "sid-999" || lc.JTI != "jti-unique-1" {
		t.Fatalf("unexpected logout claims: %+v", lc)
	}

	// Logout token containing nonce must be rejected.
	badLogout := signTestRS256(t, priv, "k1", map[string]any{
		"iss":    srv.URL + "/realms/corp",
		"aud":    "omb-control",
		"sid":    "sid-999",
		"jti":    "jti-2",
		"nonce":  "forbidden",
		"events": map[string]any{backChannelLogoutEvent: map[string]any{}},
	})
	if _, err := cl.ValidateLogoutToken(badLogout, fetchedJWKS, time.Now()); err == nil {
		t.Fatal("expected logout_token with nonce to be rejected")
	}
}
