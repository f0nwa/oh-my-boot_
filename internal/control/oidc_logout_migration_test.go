package control

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
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/control/oidcclient"
)

func TestOIDCBackChannelLogoutWithPostgreSQL(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const clientID = "omb-test"
	const sid = "fixture-session"
	const jti = "fixture-logout"
	const event = "http://schemas.openid.net/event/backchannel-logout"
	mux := http.NewServeMux()
	issuer := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(oidcclient.Discovery{
			Issuer: issuer.URL, AuthorizationEndpoint: issuer.URL + "/authorize",
			TokenEndpoint: issuer.URL + "/token", JWKSURI: issuer.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(oidcclient.JWKS{Keys: []oidcclient.JWK{{
			Kty: "RSA", Kid: "fixture", Alg: "RS256",
			N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	issuer.StartTLS()
	t.Cleanup(issuer.Close)

	cfg, err := json.Marshal(KeycloakConfig{
		PublicBaseURL: "https://omb.example.test", IssuerURL: issuer.URL, ClientID: clientID,
		CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: issuer.Certificate().Raw})),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveAuthIntegration(ctx, AuthIntegrationRecord{
		Provider: AuthSourceKeycloak, Enabled: true, ConfigJSON: cfg,
	}); err != nil {
		t.Fatal(err)
	}
	user, err := store.UpsertExternalUser(ctx, AuthSourceKeycloak, issuer.URL+"|fixture-user", "fixture-user", "Fixture User", "observer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateExternalSession(ctx, user.ID, AuthSourceKeycloak, sid); err != nil {
		t.Fatal(err)
	}
	exp := time.Now().UTC().Truncate(time.Second).Add(time.Hour)
	claims, err := json.Marshal(map[string]any{
		"iss": issuer.URL, "aud": clientID, "sid": sid, "jti": jti,
		"exp": exp.Unix(), "events": map[string]any{event: map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	payload := encode([]byte(`{"alg":"RS256","kid":"fixture"}`)) + "." + encode(claims)
	sum := sha256.Sum256([]byte(payload))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"logout_token": {payload + "." + encode(sig)}}.Encode()
	handler := (&Server{Store: store}).Handler()
	logout := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/auth/oidc/backchannel-logout", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := logout(); rec.Code != http.StatusOK {
		t.Fatalf("logout = %d: %s", rec.Code, rec.Body)
	}
	if count, err := store.CountActiveSessionsBySource(ctx, AuthSourceKeycloak); err != nil || count != 0 {
		t.Fatalf("sessions after logout = %d, err=%v", count, err)
	}
	var stored string
	if err := store.Pool.QueryRow(ctx, "SELECT jti_hash FROM oidc_logout_jti").Scan(&stored); err != nil || stored != tokenHash(jti) {
		t.Fatalf("stored identifier = %q, err=%v", stored, err)
	}
	if rec := logout(); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "replayed logout_token") {
		t.Fatalf("replay = %d: %s", rec.Code, rec.Body)
	}
}
