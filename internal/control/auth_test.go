package control

import (
	"testing"
	"time"
)

func TestAuthPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery staple" || !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	second, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == second {
		t.Fatal("salt was reused")
	}
}

func TestRolePermissions(t *testing.T) {
	for _, tc := range []struct {
		role, action string
		want         bool
	}{
		{"admin", "network", true}, {"operator", "network", false}, {"observer", "network", false},
		{"admin", "menu", true}, {"operator", "menu", true}, {"observer", "menu", false},
		{"observer", "view", true}, {"unknown", "view", false},
	} {
		if got := Allowed(tc.role, tc.action); got != tc.want {
			t.Fatalf("%s %s: %v", tc.role, tc.action, got)
		}
	}
}

func TestCSRFTokensRequireSessionSecret(t *testing.T) {
	token := CSRFToken("one-session")
	if token == "" || !CheckCSRF("one-session", token) {
		t.Fatal("token rejected")
	}
	if CheckCSRF("another-session", token) || CheckCSRF("one-session", "") {
		t.Fatal("invalid token accepted")
	}
}

func TestAuthSessionExpiry(t *testing.T) {
	if !SessionValid(time.Now().Add(time.Minute), true) {
		t.Fatal("valid session rejected")
	}
	if SessionValid(time.Now().Add(-time.Minute), true) || SessionValid(time.Now().Add(time.Minute), false) {
		t.Fatal("invalid session accepted")
	}
}
