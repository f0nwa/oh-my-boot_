package control

import (
	_ "embed"
	"net/http"
)

//go:embed login_2fa.js
var login2FAJS []byte

func login2FAScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(login2FAJS)
}
