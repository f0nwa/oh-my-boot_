package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func preparedAnswer(t *testing.T) (*WindowsISOManager, WindowsAnswerCheck, []byte) {
	t.Helper()
	profiles, answers := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(profiles, "17"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, "17", "omb-setup.cmd"), []byte("@echo off\r\nY:\\17\\sources\\setup.exe /InstallFrom:Y:\\17\\sources\\install.wim\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	xml := []byte("<unattend xmlns=\"urn:schemas-microsoft-com:unattend\">\r\n\t<!-- Привет -->\r\n</unattend>")
	sum := sha256.Sum256(xml)
	m := &WindowsISOManager{AssetRoot: profiles, AnswerRoot: answers}
	req := WindowsAnswerRequest{ID: 4, ProfileID: 17, SHA256: hex.EncodeToString(sum[:]), XML: xml}
	if err := m.PrepareAnswer(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	return m, WindowsAnswerCheck{ID: 4, ProfileID: 17, SHA256: req.SHA256}, xml
}

func postAnswerXML(h http.Handler, check WindowsAnswerCheck) *httptest.ResponseRecorder {
	body, _ := json.Marshal(check)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("POST", "/windows-answer/xml", bytes.NewReader(body)))
	return res
}

func TestWindowsAnswerXML(t *testing.T) {
	m, check, xml := preparedAnswer(t)
	h := HandlerWithManagers(nil, nil, m)

	res := postAnswerXML(h, check)
	if res.Code != http.StatusOK || !bytes.Equal(res.Body.Bytes(), xml) {
		t.Fatalf("got %d %q", res.Code, res.Body.Bytes())
	}

	other := check
	other.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if res := postAnswerXML(h, other); res.Code != http.StatusConflict {
		t.Fatalf("other hash: %d", res.Code)
	}

	missing := check
	missing.ID = 99
	if res := postAnswerXML(h, missing); res.Code != http.StatusNotFound {
		t.Fatalf("missing: %d", res.Code)
	}

	for _, bad := range []WindowsAnswerCheck{{ID: -1, ProfileID: 17, SHA256: check.SHA256}, {ID: 4, ProfileID: 17, SHA256: "../../etc/passwd"}} {
		if res := postAnswerXML(h, bad); res.Code != http.StatusBadRequest {
			t.Fatalf("%+v: %d", bad, res.Code)
		}
	}
}

func TestWindowsAnswerXMLRejectsSymlink(t *testing.T) {
	m, check, _ := preparedAnswer(t)
	final := filepath.Join(m.AnswerRoot, "4", "omb-answer.xml")
	elsewhere := filepath.Join(t.TempDir(), "x.xml")
	data, _ := os.ReadFile(final)
	if err := os.WriteFile(elsewhere, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, final); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AnswerXML(context.Background(), check); err == nil {
		t.Fatal("followed a symlink")
	}
}
