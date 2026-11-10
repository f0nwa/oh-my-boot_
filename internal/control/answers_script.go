package control

import (
	_ "embed"
	"net/http"
)

//go:embed answers.js
var answersJS []byte

func answersScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(answersJS)
}
