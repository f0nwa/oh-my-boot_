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
	body := page.Body.String()
	if !strings.Contains(body, `value="grub-test" selected`) {
		t.Fatalf("active GRUB loader not selected: %s", body)
	}
	if strings.Contains(body, `id="ipxe-options" hidden`) {
		t.Fatalf("ipxe options should not have hidden attribute when GRUB active: %s", body)
	}
	if !strings.Contains(body, `id="ipxe-options"`) || !strings.Contains(body, `aria-disabled="true"`) {
		t.Fatalf("ipxe options should be inactive (aria-disabled=true) when GRUB active: %s", body)
	}
	if strings.Contains(body, `id="grub-loader-note" style="padding:10px 14px;border:1px solid var(--line);border-radius:8px;background:var(--chip);margin:12px 0;font-size:12px;display:none;"`) {
		t.Fatalf("grub note should be displayed when GRUB active: %s", body)
	}
	if !strings.Contains(body, "Параметры iPXE не действуют при выборе GRUB") {
		t.Fatalf("grub note title missing: %s", body)
	}
	preview := httptest.NewRecorder()
	render(preview, "network_preview", data)
	previewBody := preview.Body.String()
	if !strings.Contains(previewBody, `name="uefi_loader" value="grub-test"`) {
		t.Fatalf("preview would lose GRUB selection: %s", previewBody)
	}
	if !strings.Contains(previewBody, "не используется (GRUB)") {
		t.Fatalf("preview should mark iPXE options as not used for GRUB: %s", previewBody)
	}

	dataEN := samplePageData(langEN)
	dataEN.Network.UEFILoader = "grub-test"
	pageEN := httptest.NewRecorder()
	render(pageEN, "network", dataEN)
	if !strings.Contains(pageEN.Body.String(), "iPXE parameters are inactive with GRUB") {
		t.Fatalf("page EN should display inactive note: %s", pageEN.Body.String())
	}
	previewEN := httptest.NewRecorder()
	render(previewEN, "network_preview", dataEN)
	if !strings.Contains(previewEN.Body.String(), "not used (GRUB)") {
		t.Fatalf("preview EN should mark iPXE options as not used for GRUB: %s", previewEN.Body.String())
	}
}
