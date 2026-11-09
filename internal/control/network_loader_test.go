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
	if !strings.Contains(body, "Proxy DHCP (только PXE, адреса выдаёт внешний DHCP)") ||
		!strings.Contains(body, "Полный DHCP (OMB выдаёт IP-адреса и опции PXE)") {
		t.Fatalf("page RU should display updated DHCP mode labels: %s", body)
	}
	if !strings.Contains(body, `<div class="form-actions"><button class="btn btn-primary">Сохранить</button></div>`) {
		t.Fatalf("page RU should render Save button inside form-actions: %s", body)
	}
	if strings.Contains(body, "Активный режим:") {
		t.Fatalf("page RU should not contain duplicate active mode status text: %s", body)
	}
	if strings.Contains(body, "Проверить изменения") {
		t.Fatalf("page RU should not contain old button label: %s", body)
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
	if !strings.Contains(pageEN.Body.String(), "Proxy DHCP (PXE only, external DHCP assigns addresses)") ||
		!strings.Contains(pageEN.Body.String(), "Full DHCP (OMB assigns IP addresses and PXE options)") {
		t.Fatalf("page EN should display updated DHCP mode labels: %s", pageEN.Body.String())
	}
	if !strings.Contains(pageEN.Body.String(), `<div class="form-actions"><button class="btn btn-primary">Save</button></div>`) {
		t.Fatalf("page EN should render Save button inside form-actions: %s", pageEN.Body.String())
	}
	if strings.Contains(pageEN.Body.String(), "Active mode:") {
		t.Fatalf("page EN should not contain duplicate active mode status text: %s", pageEN.Body.String())
	}
	previewEN := httptest.NewRecorder()
	render(previewEN, "network_preview", dataEN)
	if !strings.Contains(previewEN.Body.String(), "not used (GRUB)") {
		t.Fatalf("preview EN should mark iPXE options as not used for GRUB: %s", previewEN.Body.String())
	}

	dataObserver := samplePageData(langRU)
	dataObserver.Info.User.Role = "observer"
	pageObs := httptest.NewRecorder()
	render(pageObs, "network", dataObserver)
	if !strings.Contains(pageObs.Body.String(), "Параметры PXE-сети") {
		t.Fatalf("page observer RU should show network settings card: %s", pageObs.Body.String())
	}
	if strings.Contains(pageObs.Body.String(), `id="network-edit-form"`) {
		t.Fatalf("page observer RU should not have edit form: %s", pageObs.Body.String())
	}

	dataObserverEN := samplePageData(langEN)
	dataObserverEN.Info.User.Role = "observer"
	pageObsEN := httptest.NewRecorder()
	render(pageObsEN, "network", dataObserverEN)
	if !strings.Contains(pageObsEN.Body.String(), "PXE network settings") {
		t.Fatalf("page observer EN should show network settings card: %s", pageObsEN.Body.String())
	}
}
