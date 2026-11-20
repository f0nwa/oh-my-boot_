package oidcclient

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const backChannelLogoutEvent = "http://schemas.openid.net/event/backchannel-logout"

// Discovery holds the subset of OIDC Provider Metadata used by OMB.
type Discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	EndSessionEndpoint    string `json:"end_session_endpoint"`
}

// Config configures a Keycloak OIDC + Admin API client.
type Config struct {
	PublicBaseURL       string
	IssuerURL           string
	ClientID            string
	ClientSecret        string
	ServiceClientID     string
	ServiceClientSecret string
	CAPEM               string
	Timeout             time.Duration
}

// IDTokenClaims holds validated claims extracted from an OIDC ID Token.
type IDTokenClaims struct {
	Iss               string
	Sub               string
	Aud               []string
	Azp               string
	Exp               int64
	Nonce             string
	SID               string
	PreferredUsername string
	Name              string
}

// LogoutTokenClaims holds validated claims extracted from a Back-Channel Logout token.
type LogoutTokenClaims struct {
	Iss string
	Sub string
	SID string
	JTI string
	Exp int64
}

// UserStatus is the live Keycloak account status and effective client roles.
type UserStatus struct {
	Enabled     bool
	Username    string
	DisplayName string
	Roles       []string
}

// Client is a pure-Go OIDC and Keycloak Admin REST API client.
type Client struct {
	cfg  Config
	http *http.Client
}

// New creates a strict HTTPS-only OIDC client.
func New(cfg Config) (*Client, error) {
	if err := validateHTTPSURL(cfg.IssuerURL, "issuer URL"); err != nil {
		return nil, err
	}
	if cfg.PublicBaseURL != "" {
		if err := validateHTTPSURL(cfg.PublicBaseURL, "public base URL"); err != nil {
			return nil, err
		}
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if strings.TrimSpace(cfg.CAPEM) != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CAPEM)) {
			return nil, errors.New("invalid CA certificate PEM")
		}
		tlsCfg.RootCAs = pool
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				TLSClientConfig: tlsCfg,
			},
		},
	}, nil
}

func validateHTTPSURL(raw, label string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s must be a valid https:// URL", label)
	}
	return nil
}

// PKCEChallenge computes the S256 code_challenge for a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// CallbackURL returns the exact OIDC callback URL built from PublicBaseURL.
func (c *Client) CallbackURL() string {
	return strings.TrimRight(strings.TrimSpace(c.cfg.PublicBaseURL), "/") + "/auth/oidc/callback"
}

// PostLogoutURL returns the post-logout redirect URL built from PublicBaseURL.
func (c *Client) PostLogoutURL() string {
	return strings.TrimRight(strings.TrimSpace(c.cfg.PublicBaseURL), "/") + "/login"
}

// Discover fetches and validates /.well-known/openid-configuration.
func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	issuer := strings.TrimRight(strings.TrimSpace(c.cfg.IssuerURL), "/")
	endpoint := issuer + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Discovery{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Discovery{}, fmt.Errorf("oidc discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Discovery{}, fmt.Errorf("oidc discovery status %d", resp.StatusCode)
	}
	var doc Discovery
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&doc); err != nil {
		return Discovery{}, fmt.Errorf("decode discovery: %w", err)
	}
	if strings.TrimRight(doc.Issuer, "/") != issuer {
		return Discovery{}, fmt.Errorf("discovery issuer mismatch: got %q, want %q", doc.Issuer, issuer)
	}
	for _, pair := range [][2]string{
		{doc.AuthorizationEndpoint, "authorization_endpoint"},
		{doc.TokenEndpoint, "token_endpoint"},
		{doc.JWKSURI, "jwks_uri"},
	} {
		if err := validateHTTPSURL(pair[0], pair[1]); err != nil {
			return Discovery{}, err
		}
	}
	return doc, nil
}

// FetchJWKS downloads the provider JWKS.
func (c *Client) FetchJWKS(ctx context.Context, jwksURI string) (JWKS, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURI, nil)
	if err != nil {
		return JWKS{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return JWKS{}, fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return JWKS{}, fmt.Errorf("jwks status %d", resp.StatusCode)
	}
	var jwks JWKS
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(&jwks); err != nil {
		return JWKS{}, fmt.Errorf("decode jwks: %w", err)
	}
	if len(jwks.Keys) == 0 {
		return JWKS{}, errors.New("jwks contains no keys")
	}
	return jwks, nil
}

// AuthURL builds the authorization redirect URL with PKCE S256, state, and nonce.
func (c *Client) AuthURL(doc Discovery, state, nonce, codeVerifier string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.cfg.ClientID},
		"redirect_uri":          {c.CallbackURL()},
		"scope":                 {"openid profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {PKCEChallenge(codeVerifier)},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(doc.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return doc.AuthorizationEndpoint + sep + q.Encode()
}

// ExchangeCode exchanges an authorization code + PKCE verifier for an ID Token.
func (c *Client) ExchangeCode(ctx context.Context, doc Discovery, code, codeVerifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.CallbackURL()},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, doc.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(&tok); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if tok.IDToken == "" {
		return "", errors.New("token response missing id_token")
	}
	return tok.IDToken, nil
}

// ValidateIDToken verifies signature and OIDC Core claims (iss, aud, azp, exp, nonce).
func (c *Client) ValidateIDToken(idToken string, jwks JWKS, expectedNonce string, now time.Time) (IDTokenClaims, error) {
	raw, err := VerifyJWT(idToken, jwks)
	if err != nil {
		return IDTokenClaims{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return IDTokenClaims{}, fmt.Errorf("decode id_token claims: %w", err)
	}
	claims := IDTokenClaims{
		Iss:               strClaim(m, "iss"),
		Sub:               strClaim(m, "sub"),
		Aud:               audClaim(m["aud"]),
		Azp:               strClaim(m, "azp"),
		Exp:               int64Claim(m, "exp"),
		Nonce:             strClaim(m, "nonce"),
		SID:               strClaim(m, "sid"),
		PreferredUsername: strClaim(m, "preferred_username"),
		Name:              strClaim(m, "name"),
	}
	wantIss := strings.TrimRight(strings.TrimSpace(c.cfg.IssuerURL), "/")
	if strings.TrimRight(claims.Iss, "/") != wantIss {
		return IDTokenClaims{}, errors.New("id_token issuer mismatch")
	}
	if claims.Sub == "" {
		return IDTokenClaims{}, errors.New("id_token missing sub")
	}
	if !containsAudience(claims.Aud, c.cfg.ClientID) {
		return IDTokenClaims{}, errors.New("id_token audience mismatch")
	}
	if len(claims.Aud) > 1 && claims.Azp != c.cfg.ClientID {
		return IDTokenClaims{}, errors.New("id_token azp mismatch")
	}
	if claims.Exp <= now.Unix() {
		return IDTokenClaims{}, errors.New("id_token expired")
	}
	if expectedNonce != "" && claims.Nonce != expectedNonce {
		return IDTokenClaims{}, errors.New("id_token nonce mismatch")
	}
	return claims, nil
}

// ValidateLogoutToken verifies an OIDC Back-Channel Logout JWT.
func (c *Client) ValidateLogoutToken(logoutToken string, jwks JWKS, now time.Time) (LogoutTokenClaims, error) {
	raw, err := VerifyJWT(logoutToken, jwks)
	if err != nil {
		return LogoutTokenClaims{}, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return LogoutTokenClaims{}, fmt.Errorf("decode logout_token claims: %w", err)
	}
	if _, hasNonce := m["nonce"]; hasNonce {
		return LogoutTokenClaims{}, errors.New("logout_token must not contain nonce")
	}
	events, ok := m["events"].(map[string]any)
	if !ok {
		return LogoutTokenClaims{}, errors.New("logout_token missing events claim")
	}
	if _, ok := events[backChannelLogoutEvent]; !ok {
		return LogoutTokenClaims{}, errors.New("logout_token missing backchannel-logout event")
	}
	claims := LogoutTokenClaims{
		Iss: strClaim(m, "iss"),
		Sub: strClaim(m, "sub"),
		SID: strClaim(m, "sid"),
		JTI: strClaim(m, "jti"),
		Exp: int64Claim(m, "exp"),
	}
	wantIss := strings.TrimRight(strings.TrimSpace(c.cfg.IssuerURL), "/")
	if strings.TrimRight(claims.Iss, "/") != wantIss {
		return LogoutTokenClaims{}, errors.New("logout_token issuer mismatch")
	}
	if !containsAudience(audClaim(m["aud"]), c.cfg.ClientID) {
		return LogoutTokenClaims{}, errors.New("logout_token audience mismatch")
	}
	if claims.JTI == "" {
		return LogoutTokenClaims{}, errors.New("logout_token missing jti")
	}
	if claims.SID == "" && claims.Sub == "" {
		return LogoutTokenClaims{}, errors.New("logout_token missing sid and sub")
	}
	if claims.Exp > 0 && claims.Exp <= now.Unix() {
		return LogoutTokenClaims{}, errors.New("logout_token expired")
	}
	return claims, nil
}

// LogoutURL builds the RP-Initiated Logout URL.
func (c *Client) LogoutURL(doc Discovery) string {
	if doc.EndSessionEndpoint == "" {
		return ""
	}
	q := url.Values{
		"client_id":                {c.cfg.ClientID},
		"post_logout_redirect_uri": {c.PostLogoutURL()},
	}
	sep := "?"
	if strings.Contains(doc.EndSessionEndpoint, "?") {
		sep = "&"
	}
	return doc.EndSessionEndpoint + sep + q.Encode()
}

// FetchUserStatus queries Keycloak Admin REST API using the service client to read
// live user enabled state and effective client roles for cfg.ClientID.
func (c *Client) FetchUserStatus(ctx context.Context, doc Discovery, sub string) (UserStatus, error) {
	token, err := c.fetchServiceToken(ctx, doc.TokenEndpoint)
	if err != nil {
		return UserStatus{}, err
	}
	adminBase, err := keycloakAdminRealmBase(c.cfg.IssuerURL)
	if err != nil {
		return UserStatus{}, err
	}
	clientUUID, err := c.resolveClientUUID(ctx, adminBase, token, c.cfg.ClientID)
	if err != nil {
		return UserStatus{}, err
	}

	// 1. User status
	var uResp struct {
		Enabled   bool   `json:"enabled"`
		Username  string `json:"username"`
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	}
	userURL := adminBase + "/users/" + url.PathEscape(sub)
	statusCode, err := c.adminGETJSON(ctx, userURL, token, &uResp)
	if statusCode == http.StatusNotFound {
		return UserStatus{Enabled: false}, nil
	}
	if err != nil {
		return UserStatus{}, err
	}
	display := strings.TrimSpace(uResp.FirstName + " " + uResp.LastName)
	if !uResp.Enabled {
		return UserStatus{Enabled: false, Username: uResp.Username, DisplayName: display}, nil
	}

	// 2. Effective client roles (composite includes inherited group/composite roles)
	var rolesResp []struct {
		Name string `json:"name"`
	}
	rolesURL := adminBase + "/users/" + url.PathEscape(sub) + "/role-mappings/clients/" + url.PathEscape(clientUUID) + "/composite"
	if _, err := c.adminGETJSON(ctx, rolesURL, token, &rolesResp); err != nil {
		return UserStatus{}, err
	}
	roles := make([]string, 0, len(rolesResp))
	for _, r := range rolesResp {
		roles = append(roles, r.Name)
	}
	return UserStatus{
		Enabled:     true,
		Username:    uResp.Username,
		DisplayName: display,
		Roles:       roles,
	}, nil
}

// TestServiceAPI verifies that the service client can authenticate and locate cfg.ClientID in Admin REST API.
func (c *Client) TestServiceAPI(ctx context.Context, doc Discovery) error {
	token, err := c.fetchServiceToken(ctx, doc.TokenEndpoint)
	if err != nil {
		return err
	}
	adminBase, err := keycloakAdminRealmBase(c.cfg.IssuerURL)
	if err != nil {
		return err
	}
	_, err = c.resolveClientUUID(ctx, adminBase, token, c.cfg.ClientID)
	return err
}

func (c *Client) fetchServiceToken(ctx context.Context, tokenEndpoint string) (string, error) {
	if c.cfg.ServiceClientID == "" || c.cfg.ServiceClientSecret == "" {
		return "", errors.New("Keycloak service client credentials are required")
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.cfg.ServiceClientID},
		"client_secret": {c.cfg.ServiceClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("service token request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("service token status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&tok); err != nil {
		return "", err
	}
	if tok.AccessToken == "" {
		return "", errors.New("service token response missing access_token")
	}
	return tok.AccessToken, nil
}

func (c *Client) resolveClientUUID(ctx context.Context, adminBase, token, clientID string) (string, error) {
	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	}
	endpoint := adminBase + "/clients?clientId=" + url.QueryEscape(clientID)
	if _, err := c.adminGETJSON(ctx, endpoint, token, &clients); err != nil {
		return "", err
	}
	for _, cl := range clients {
		if cl.ClientID == clientID && cl.ID != "" {
			return cl.ID, nil
		}
	}
	return "", fmt.Errorf("Keycloak client %q not found via Admin API", clientID)
}

func (c *Client) adminGETJSON(ctx context.Context, endpoint, bearer string, dst any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("keycloak admin api: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, fmt.Errorf("keycloak admin api status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 512<<10)).Decode(dst); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// keycloakAdminRealmBase converts https://host/realms/myrealm -> https://host/admin/realms/myrealm.
func keycloakAdminRealmBase(issuerURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(issuerURL), "/"))
	if err != nil {
		return "", err
	}
	idx := strings.LastIndex(u.Path, "/realms/")
	if idx < 0 {
		return "", fmt.Errorf("cannot derive Keycloak Admin API URL from issuer %q", issuerURL)
	}
	u.Path = u.Path[:idx] + "/admin" + u.Path[idx:]
	return u.String(), nil
}

func strClaim(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func int64Claim(m map[string]any, key string) int64 {
	if f, ok := m[key].(float64); ok {
		return int64(f)
	}
	return 0
}

func audClaim(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func containsAudience(aud []string, want string) bool {
	for _, a := range aud {
		if a == want {
			return true
		}
	}
	return false
}
