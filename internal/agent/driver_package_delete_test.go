package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveDriverPackageRejectsInvalidHashAndSymlink(t *testing.T) {
	root, outside := canonicalTestDir(t), canonicalTestDir(t)
	hash := strings.Repeat("a", 64)
	sentinel := filepath.Join(outside, "keep")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, hash)); err != nil {
		t.Fatal(err)
	}
	handler := HandlerWithDriverManager(nil, nil, nil, &DriverManager{Root: root})
	for _, tc := range []struct {
		hash   string
		status int
	}{{"../unsafe", 400}, {"3", 400}, {hash, 503}} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/winpe-drivers/remove-package", strings.NewReader(`{"sha256":"`+tc.hash+`"}`)))
		if res.Code != tc.status {
			t.Errorf("%s: status=%d", tc.hash, res.Code)
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("external file removed", err)
	}
}
