package control

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNetworkPageCarriesGRUBSelectionThroughPreview(t *testing.T) {
	form := strings.NewReader("uefi_loader=grub-test")
	req := httptest.NewRequest("POST", "/network/preview", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := networkForm(req).UEFILoader; got != "grub-test" {
		t.Fatalf("form lost UEFI loader: %q", got)
	}
	data := samplePageData(langRU)
	data.Network.UEFILoader = "grub-test"
	page := httptest.NewRecorder()
	render(page, "network", data)
	if !strings.Contains(page.Body.String(), `value="grub-test" selected`) {
		t.Fatalf("active GRUB loader not selected: %s", page.Body.String())
	}
	preview := httptest.NewRecorder()
	render(preview, "network_preview", data)
	if !strings.Contains(preview.Body.String(), `name="uefi_loader" value="grub-test"`) {
		t.Fatalf("preview would lose GRUB selection: %s", preview.Body.String())
	}
}
