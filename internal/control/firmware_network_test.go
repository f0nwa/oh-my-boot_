package control

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"omb/internal/network"
)

func TestFirmwareNetworkSettingSurvivesPreviewAndConfirmation(t *testing.T) {
	form := url.Values{"reuse_firmware_network": {"true"}}
	r := httptest.NewRequest("POST", "/network/preview", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := networkForm(r)
	data, _ := json.Marshal(c)
	if !strings.Contains(string(data), `"reuse_firmware_network":true`) {
		t.Fatalf("setting lost by form: %s", data)
	}
	confirmed := networkConfirmation("token", c)
	if confirmed == networkConfirmation("token", network.Config{}) {
		t.Fatal("confirmation does not bind firmware setting")
	}
	for _, value := range []string{"", "false"} {
		form.Set("reuse_firmware_network", value)
		r := httptest.NewRequest("POST", "/network/preview", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if networkForm(r).ReuseFirmwareNetwork {
			t.Fatalf("cannot disable via form value %q", value)
		}
	}
	for _, lang := range []string{"ru", "en"} {
		page := samplePageData(lang)
		page.Network = c
		w := httptest.NewRecorder()
		render(w, "network", page)
		if !strings.Contains(w.Body.String(), `name="reuse_firmware_network" value="true" checked`) {
			t.Fatalf("%s: active setting not checked", lang)
		}
		w = httptest.NewRecorder()
		render(w, "network_preview", page)
		if !strings.Contains(w.Body.String(), `name="reuse_firmware_network" value="true"`) {
			t.Fatalf("%s: preview drops setting", lang)
		}
		for _, role := range []string{"observer", "operator"} {
			page.Info.User.Role = role
			w = httptest.NewRecorder()
			render(w, "network", page)
			if strings.Contains(w.Body.String(), `name="reuse_firmware_network"`) {
				t.Fatalf("%s can edit network", role)
			}
		}
	}
}
