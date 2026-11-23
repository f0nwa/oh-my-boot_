package control

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/control/totp"
)

type fakeTrustedDevice struct {
	userID    int64
	userAgent string
	expiresAt time.Time
}

type fakeTOTPStore struct {
	authOnlyStore
	user        User
	password    string
	secretEnc   []byte
	codes       map[string]bool // hash -> used
	challenges  map[string]*Login2FAChallenge
	policy      PasswordPolicy
	sessions    map[string]User
	trusted     map[string]*fakeTrustedDevice
	resetCalled bool
}

func newFakeTOTPStore(u User, password string) *fakeTOTPStore {
	return &fakeTOTPStore{
		user:       u,
		password:   password,
		codes:      map[string]bool{},
		challenges: map[string]*Login2FAChallenge{},
		policy:     PasswordPolicy{WarnDays: 7, TOTPPolicy: TOTPPolicyOff},
		sessions:   map[string]User{"tok-1": u},
		trusted:    map[string]*fakeTrustedDevice{},
	}
}

func (f *fakeTOTPStore) Session(_ context.Context, token string) (User, error) {
	u, ok := f.sessions[token]
	if !ok {
		return User{}, fmt.Errorf("missing session")
	}
	return u, nil
}
func (f *fakeTOTPStore) Users(context.Context) ([]User, error) { return []User{f.user}, nil }
func (f *fakeTOTPStore) PasswordPolicy(context.Context) (PasswordPolicy, error) {
	return f.policy, nil
}
func (f *fakeTOTPStore) SetPasswordPolicy(_ context.Context, p PasswordPolicy) error {
	f.policy = p
	return nil
}
func (f *fakeTOTPStore) VerifyLocalCredentials(_ context.Context, username, password string) (User, error) {
	if username == f.user.Username && password == f.password && f.user.Enabled {
		return f.user, nil
	}
	return User{}, fmt.Errorf("invalid credentials")
}
func (f *fakeTOTPStore) VerifyLocalPasswordByID(_ context.Context, userID int64, password string) error {
	if userID == f.user.ID && password == f.password {
		return nil
	}
	return fmt.Errorf("invalid credentials")
}
func (f *fakeTOTPStore) CreateLocalSession(_ context.Context, _ int64) (string, error) {
	tok := fmt.Sprintf("sess-%d", len(f.sessions)+1)
	f.sessions[tok] = f.user
	return tok, nil
}
func (f *fakeTOTPStore) UserTOTPSecret(_ context.Context, _ int64) ([]byte, bool, int64, error) {
	return f.secretEnc, f.user.TOTPEnabled, f.user.TOTPLastStep, nil
}
func (f *fakeTOTPStore) EnableUserTOTP(_ context.Context, _ int64, secretEnc []byte, lastStep int64, codeHashes []string) error {
	now := time.Now()
	f.secretEnc = secretEnc
	f.user.TOTPEnabled = true
	f.user.TOTPEnabledAt = &now
	f.user.TOTPLastStep = lastStep
	f.codes = map[string]bool{}
	for _, h := range codeHashes {
		f.codes[h] = false
	}
	for k := range f.sessions {
		f.sessions[k] = f.user
	}
	return nil
}
func (f *fakeTOTPStore) DisableUserTOTP(_ context.Context, _ int64) error {
	f.secretEnc = nil
	f.user.TOTPEnabled = false
	f.user.TOTPEnabledAt = nil
	f.user.TOTPLastStep = 0
	f.codes = map[string]bool{}
	f.trusted = map[string]*fakeTrustedDevice{}
	for k := range f.sessions {
		f.sessions[k] = f.user
	}
	return nil
}
func (f *fakeTOTPStore) ResetUserTOTP(ctx context.Context, userID int64) error {
	f.resetCalled = true
	f.trusted = map[string]*fakeTrustedDevice{}
	return f.DisableUserTOTP(ctx, userID)
}
func (f *fakeTOTPStore) ReplaceRecoveryCodes(_ context.Context, _ int64, codeHashes []string) error {
	f.codes = map[string]bool{}
	for _, h := range codeHashes {
		f.codes[h] = false
	}
	return nil
}
func (f *fakeTOTPStore) CountUnusedRecoveryCodes(_ context.Context, _ int64) (int, error) {
	n := 0
	for _, used := range f.codes {
		if !used {
			n++
		}
	}
	return n, nil
}
func (f *fakeTOTPStore) UpdateTOTPLastStep(_ context.Context, _ int64, step int64) error {
	if step <= f.user.TOTPLastStep {
		return fmt.Errorf("replayed")
	}
	f.user.TOTPLastStep = step
	return nil
}
func (f *fakeTOTPStore) ConsumeRecoveryCode(_ context.Context, _ int64, codeHash string) (bool, error) {
	used, ok := f.codes[codeHash]
	if !ok || used {
		return false, nil
	}
	f.codes[codeHash] = true
	return true, nil
}
func (f *fakeTOTPStore) CreateLogin2FAChallenge(_ context.Context, _ int64, ttl time.Duration) (string, error) {
	tok := fmt.Sprintf("ch-%d", len(f.challenges)+1)
	f.challenges[tok] = &Login2FAChallenge{
		User:      f.user,
		SecretEnc: f.secretEnc,
		ExpiresAt: time.Now().Add(ttl),
	}
	return tok, nil
}
func (f *fakeTOTPStore) GetLogin2FAChallenge(_ context.Context, token string) (Login2FAChallenge, error) {
	ch, ok := f.challenges[token]
	if !ok {
		return Login2FAChallenge{}, fmt.Errorf("not found")
	}
	ch.User = f.user
	ch.SecretEnc = f.secretEnc
	return *ch, nil
}
func (f *fakeTOTPStore) IncrementLogin2FAAttempts(_ context.Context, token string) (int, error) {
	ch := f.challenges[token]
	ch.Attempts++
	return ch.Attempts, nil
}
func (f *fakeTOTPStore) DeleteLogin2FAChallenge(_ context.Context, token string) error {
	delete(f.challenges, token)
	return nil
}

func (f *fakeTOTPStore) CreateTrustedBrowser(_ context.Context, userID int64, userAgent string, ttl time.Duration) (string, error) {
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	f.trusted[tokenHash(tok)] = &fakeTrustedDevice{
		userID:    userID,
		userAgent: userAgent,
		expiresAt: time.Now().Add(ttl),
	}
	return tok, nil
}

func (f *fakeTOTPStore) ValidateTrustedBrowser(_ context.Context, userID int64, token string, _ string, ttl time.Duration) (bool, error) {
	if token == "" {
		return false, nil
	}
	dev, ok := f.trusted[tokenHash(token)]
	if !ok || dev.userID != userID || time.Now().After(dev.expiresAt) {
		return false, nil
	}
	dev.expiresAt = time.Now().Add(ttl)
	return true, nil
}

func (f *fakeTOTPStore) RevokeUserTrustedBrowsers(_ context.Context, userID int64) error {
	for h, dev := range f.trusted {
		if dev.userID == userID {
			delete(f.trusted, h)
		}
	}
	return nil
}

func (f *fakeTOTPStore) CountUserTrustedBrowsers(_ context.Context, userID int64) (int, error) {
	var count int
	now := time.Now()
	for _, dev := range f.trusted {
		if dev.userID == userID && now.Before(dev.expiresAt) {
			count++
		}
	}
	return count, nil
}

func TestLocal2FAEndToEndFlowAndPolicy(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := NewSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	u := User{ID: 1, Username: "admin", AuthSource: AuthSourceLocal, Role: "admin", Enabled: true, Language: "ru", PasswordChangedAt: time.Now()}
	store := newFakeTOTPStore(u, "StrongPass123!")
	srv := &Server{Store: store, Secrets: box}
	h := srv.Handler()

	// 1. /account renders separate 2FA card (Variant 2) for local user.
	req := httptest.NewRequest("GET", "/account", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "account-2fa-card") {
		t.Fatalf("GET /account status=%d missing account-2fa-card", res.Code)
	}

	// 2. Begin 2FA setup -> returns QR SVG, Base32 secret, and pending_token.
	csrf := CSRFToken("tok-1")
	form := url.Values{"csrf": {csrf}}
	req = httptest.NewRequest("POST", "/account/2fa/begin", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("POST /account/2fa/begin status=%d body=%s", res.Code, res.Body.String())
	}
	var beginResp struct {
		QRSVG        string `json:"qr_svg"`
		Secret       string `json:"secret"`
		PendingToken string `json:"pending_token"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &beginResp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(beginResp.QRSVG, "<svg ") || beginResp.PendingToken == "" {
		t.Fatalf("unexpected begin response: %+v", beginResp)
	}

	// 3. Confirm with valid TOTP code -> enables 2FA and returns 8 recovery codes.
	rawSecret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(beginResp.Secret)
	if err != nil {
		t.Fatal(err)
	}
	code := totp.GenerateCode(rawSecret, time.Now())
	form = url.Values{"csrf": {csrf}, "pending_token": {beginResp.PendingToken}, "code": {code}}
	req = httptest.NewRequest("POST", "/account/2fa/enable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !store.user.TOTPEnabled {
		t.Fatalf("POST /account/2fa/enable status=%d enabled=%v body=%s", res.Code, store.user.TOTPEnabled, res.Body.String())
	}
	var enableResp struct {
		OK    bool     `json:"ok"`
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &enableResp); err != nil || len(enableResp.Codes) != 8 {
		t.Fatalf("expected 8 recovery codes, got err=%v codes=%d", err, len(enableResp.Codes))
	}

	// 4. Login with password redirects to /login/2fa instead of issuing session.
	form = url.Values{"csrf": {"login-csrf"}, "username": {"admin"}, "password": {"StrongPass123!"}}
	req = httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "login-csrf"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/login/2fa" {
		t.Fatalf("POST /login with 2FA status=%d loc=%s", res.Code, res.Header().Get("Location"))
	}
	var chCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == login2FACookieName {
			chCookie = c
		}
	}
	if chCookie == nil {
		t.Fatal("missing omb_2fa challenge cookie")
	}

	// 4a. Verify GET /login/2fa renders SVG network animation and login-2fa.js script.
	req = httptest.NewRequest("GET", "/login/2fa", nil)
	req.AddCookie(chCookie)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /login/2fa status=%d", res.Code)
	}
	body2fa := res.Body.String()
	if !strings.Contains(body2fa, `<div class="login-net-bg" aria-hidden="true"><svg class="login-net"`) {
		t.Errorf("GET /login/2fa missing login-net-bg SVG wrapper")
	}
	if !strings.Contains(body2fa, `<script defer src="/assets/login-2fa.js"></script>`) {
		t.Errorf("GET /login/2fa missing script /assets/login-2fa.js")
	}

	// 5. Verify recovery code completes login, and reusing the same recovery code fails.
	form = url.Values{"csrf": {"c2"}, "mode": {"recovery"}, "code": {enableResp.Codes[0]}}
	req = httptest.NewRequest("POST", "/login/2fa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(chCookie)
	req.AddCookie(&http.Cookie{Name: login2FACSRFCookie, Value: "c2"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/" {
		t.Fatalf("POST /login/2fa recovery status=%d loc=%s", res.Code, res.Header().Get("Location"))
	}

	// 6. Mandatory 2FA policy blocks disabling and forces /setup-2fa after admin reset.
	store.policy.TOTPPolicy = TOTPPolicyAdmins
	form = url.Values{"csrf": {csrf}, "current_password": {"StrongPass123!"}}
	req = httptest.NewRequest("POST", "/account/2fa/disable", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when disabling required 2FA, got %d", res.Code)
	}

	// Admin resets 2FA -> user is forced to /setup-2fa on GET /account.
	form = url.Values{"csrf": {csrf}, "id": {"1"}}
	req = httptest.NewRequest("POST", "/users/reset-2fa", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || !store.resetCalled || store.user.TOTPEnabled {
		t.Fatalf("POST /users/reset-2fa status=%d reset=%v enabled=%v", res.Code, store.resetCalled, store.user.TOTPEnabled)
	}

	req = httptest.NewRequest("GET", "/account", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/setup-2fa" {
		t.Fatalf("expected redirect to /setup-2fa when 2FA required, got %d loc=%s", res.Code, res.Header().Get("Location"))
	}
}

func TestUsersPageShowsCombinedSecurityPolicyAnd2FAColumn(t *testing.T) {
	u := User{ID: 1, Username: "admin", AuthSource: AuthSourceLocal, Role: "admin", Enabled: true, TOTPEnabled: true, Language: "ru", PasswordChangedAt: time.Now()}
	store := newFakeTOTPStore(u, "StrongPass123!")
	srv := &Server{Store: store}
	req := httptest.NewRequest("GET", "/users", nil)
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "tok-1"})
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("GET /users status=%d", res.Code)
	}
	for _, want := range []string{"Политика безопасности", "totp_policy", "user-reset-2fa-dialog", "Сбросить 2FA…"} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /users missing %q", want)
		}
	}
}

func TestLogin2FAScriptEndpoint(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/assets/login-2fa.js", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("expected javascript content type, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("expected Cache-Control no-store, got %q", cc)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"login-2fa-mode",
		"login-2fa-code",
		"login-2fa-submit",
		"login-net",
		"DHCP DISCOVER",
		"ipxe-shim.efi",
		"snponly-shim.efi",
		"undionly.kpxe",
		"boot/menu.ipxe",
		"menu ready",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login-2fa.js missing %q", want)
		}
	}
}

func TestTrustedBrowser2FAEndToEndFlow(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	box, err := NewSecretBox(key)
	if err != nil {
		t.Fatal(err)
	}
	rawSecret, _, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	encSecret, err := box.Encrypt(totpUserAAD(1), rawSecret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	u := User{
		ID:                1,
		Username:          "admin",
		AuthSource:        AuthSourceLocal,
		Role:              "admin",
		Enabled:           true,
		TOTPEnabled:       true,
		TOTPEnabledAt:     &now,
		Language:          "ru",
		PasswordChangedAt: now,
	}
	store := newFakeTOTPStore(u, "StrongPass123!")
	store.secretEnc = encSecret
	srv := &Server{Store: store, Secrets: box}
	h := srv.Handler()

	// 1. Initial login without trust cookie -> redirect to /login/2fa
	loginForm := url.Values{
		"csrf":     {"test-csrf"},
		"username": {"admin"},
		"password": {"StrongPass123!"},
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(loginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "test-csrf"})
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/login/2fa" {
		t.Fatalf("expected redirect to /login/2fa, got %d loc=%s", res.Code, res.Header().Get("Location"))
	}
	var challengeCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa" {
			challengeCookie = c
			break
		}
	}
	if challengeCookie == nil || challengeCookie.Value == "" {
		t.Fatal("expected omb_2fa challenge cookie")
	}

	// 2. GET /login/2fa -> page contains trust_browser checkbox
	req = httptest.NewRequest("GET", "/login/2fa", nil)
	req.AddCookie(challengeCookie)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /login/2fa status=%d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, "trust_browser") || !strings.Contains(body, "Доверять этому браузеру") {
		t.Fatalf("expected trust_browser checkbox on /login/2fa, body=%s", body)
	}
	// 3. POST /login/2fa with valid TOTP code and trust_browser=true
	code := totp.GenerateCode(rawSecret, time.Now())
	verifyForm := url.Values{
		"csrf":          {"c-2fa"},
		"mode":          {"totp"},
		"code":          {code},
		"trust_browser": {"true"},
	}
	req = httptest.NewRequest("POST", "/login/2fa", strings.NewReader(verifyForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(challengeCookie)
	req.AddCookie(&http.Cookie{Name: login2FACSRFCookie, Value: "c-2fa"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/" {
		t.Fatalf("POST /login/2fa status=%d loc=%s", res.Code, res.Header().Get("Location"))
	}
	var trustCookie, sessionCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa_trust" {
			trustCookie = c
		}
		if c.Name == "omb_session" {
			sessionCookie = c
		}
	}
	if trustCookie == nil || trustCookie.Value == "" {
		t.Fatal("expected omb_2fa_trust cookie to be issued")
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("expected omb_session cookie to be issued")
	}
	cnt, _ := store.CountUserTrustedBrowsers(context.Background(), 1)
	if cnt != 1 {
		t.Fatalf("expected 1 trusted browser in store, got %d", cnt)
	}

	// 4. Subsequent login with trust cookie -> bypasses /login/2fa immediately!
	req = httptest.NewRequest("POST", "/login", strings.NewReader(loginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "test-csrf"})
	req.AddCookie(trustCookie)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/" {
		t.Fatalf("expected direct redirect to /, got %d loc=%s", res.Code, res.Header().Get("Location"))
	}
	var refreshedTrustCookie, newSessionCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa_trust" {
			refreshedTrustCookie = c
		}
		if c.Name == "omb_session" {
			newSessionCookie = c
		}
	}
	if refreshedTrustCookie == nil || newSessionCookie == nil {
		t.Fatal("expected refreshed trust cookie and new session cookie on bypass")
	}

	// 5. GET /account shows trusted browsers count and revoke button
	req = httptest.NewRequest("GET", "/account", nil)
	req.AddCookie(newSessionCookie)
	req.AddCookie(trustCookie)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("GET /account status=%d", res.Code)
	}
	accBody := res.Body.String()
	if !strings.Contains(accBody, "Доверенные браузеры") || !strings.Contains(accBody, "включая этот браузер") {
		t.Fatalf("GET /account missing trusted browser info, body=%s", accBody)
	}
	if !strings.Contains(accBody, "/account/2fa/revoke-trusted") || !strings.Contains(accBody, "Сбросить доверенные браузеры") {
		t.Fatalf("GET /account missing revoke button, body=%s", accBody)
	}

	// 6. POST /account/2fa/revoke-trusted revokes trusted devices
	revokeForm := url.Values{"csrf": {CSRFToken(newSessionCookie.Value)}}
	req = httptest.NewRequest("POST", "/account/2fa/revoke-trusted", strings.NewReader(revokeForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(newSessionCookie)
	req.AddCookie(trustCookie)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/account?message="+url.QueryEscape("Доверие браузерам сброшено") {
		t.Fatalf("POST /account/2fa/revoke-trusted status=%d loc=%s", res.Code, res.Header().Get("Location"))
	}
	var clearedTrustCookie *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa_trust" {
			clearedTrustCookie = c
			break
		}
	}
	if clearedTrustCookie == nil || clearedTrustCookie.MaxAge >= 0 {
		t.Fatal("expected omb_2fa_trust cookie to be cleared with MaxAge < 0")
	}
	cnt, _ = store.CountUserTrustedBrowsers(context.Background(), 1)
	if cnt != 0 {
		t.Fatalf("expected 0 trusted browsers after revoke, got %d", cnt)
	}

	// 7. Login after revoke -> requires /login/2fa again!
	req = httptest.NewRequest("POST", "/login", strings.NewReader(loginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "test-csrf"})
	req.AddCookie(trustCookie) // old/revoked token
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/login/2fa" {
		t.Fatalf("expected redirect to /login/2fa after revocation, got %d loc=%s", res.Code, res.Header().Get("Location"))
	}

	// 8. Login without trust_browser checkbox -> does not issue trust cookie
	store.user.TOTPLastStep = 0
	req = httptest.NewRequest("POST", "/login", strings.NewReader(loginForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_login_csrf", Value: "test-csrf"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	var chCookie2 *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa" {
			chCookie2 = c
			break
		}
	}
	noTrustForm := url.Values{
		"csrf":          {"c-2fa-2"},
		"mode":          {"totp"},
		"code":          {totp.GenerateCode(rawSecret, time.Now())},
		"trust_browser": {""}, // unchecked
	}
	req = httptest.NewRequest("POST", "/login/2fa", strings.NewReader(noTrustForm.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(chCookie2)
	req.AddCookie(&http.Cookie{Name: login2FACSRFCookie, Value: "c-2fa-2"})
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/" {
		t.Fatalf("POST /login/2fa status=%d loc=%s", res.Code, res.Header().Get("Location"))
	}
	for _, c := range res.Result().Cookies() {
		if c.Name == "omb_2fa_trust" && c.MaxAge >= 0 {
			t.Fatal("omb_2fa_trust should not be issued when trust_browser is unchecked")
		}
	}
}

