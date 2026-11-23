package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNetworkPageRendersSettingsAndPreview(t *testing.T) {
	form := strings.NewReader("default_driver=snp&reuse_firmware_network=true")
	req := httptest.NewRequest("POST", "/network/preview", form)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	parsed := networkForm(req)
	if parsed.DefaultDriver != "snp" || !parsed.ReuseFirmwareNetwork {
		t.Fatalf("form lost iPXE driver settings: %+v", parsed)
	}
	data := samplePageData(langRU)
	data.Network.DefaultDriver = "snp"
	data.Network.ReuseFirmwareNetwork = true
	page := httptest.NewRecorder()
	render(page, "network", data)
	body := page.Body.String()
	if strings.Contains(body, `name="uefi_loader"`) || strings.Contains(body, "grub-test") {
		t.Fatalf("page RU should not contain removed GRUB loader selector: %s", body)
	}
	if !strings.Contains(body, "Proxy DHCP (только PXE, адреса выдаёт внешний DHCP)") ||
		!strings.Contains(body, "Полный DHCP (OMB выдаёт IP-адреса и опции PXE)") {
		t.Fatalf("page RU should display updated DHCP mode labels: %s", body)
	}
	if !strings.Contains(body, `<div class="form-actions"><button class="btn btn-primary" disabled>Сохранить</button></div>`) {
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
	if strings.Contains(previewBody, `name="uefi_loader"`) || strings.Contains(previewBody, "GRUB") {
		t.Fatalf("preview RU should not contain removed GRUB references: %s", previewBody)
	}
	if !strings.Contains(previewBody, `<div class="form-actions"><button class="btn btn-primary">Применить</button><a class="btn" href="/network">Отмена</a></div>`) {
		t.Fatalf("preview RU should render Apply and Cancel buttons inside form-actions: %s", previewBody)
	}

	dataEN := samplePageData(langEN)
	dataEN.Network.DefaultDriver = "snp"
	pageEN := httptest.NewRecorder()
	render(pageEN, "network", dataEN)
	if strings.Contains(pageEN.Body.String(), "GRUB") {
		t.Fatalf("page EN should not mention GRUB: %s", pageEN.Body.String())
	}
	if !strings.Contains(pageEN.Body.String(), "Proxy DHCP (PXE only, external DHCP assigns addresses)") ||
		!strings.Contains(pageEN.Body.String(), "Full DHCP (OMB assigns IP addresses and PXE options)") {
		t.Fatalf("page EN should display updated DHCP mode labels: %s", pageEN.Body.String())
	}
	if !strings.Contains(pageEN.Body.String(), `<div class="form-actions"><button class="btn btn-primary" disabled>Save</button></div>`) {
		t.Fatalf("page EN should render Save button inside form-actions: %s", pageEN.Body.String())
	}
	if strings.Contains(pageEN.Body.String(), "Active mode:") {
		t.Fatalf("page EN should not contain duplicate active mode status text: %s", pageEN.Body.String())
	}
	previewEN := httptest.NewRecorder()
	render(previewEN, "network_preview", dataEN)
	if !strings.Contains(previewEN.Body.String(), `<div class="form-actions"><button class="btn btn-primary">Apply</button><a class="btn" href="/network">Cancel</a></div>`) {
		t.Fatalf("preview EN should render Apply and Cancel buttons inside form-actions: %s", previewEN.Body.String())
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

func TestNetworkPageBrowser(t *testing.T) {
	if os.Getenv("OMB_UI_TEST") != "1" {
		t.Skip("set OMB_UI_TEST=1 and NODE_PATH to run browser check")
	}
	dir := t.TempDir()
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)"`)
	s := &Server{}
	for _, lang := range []string{langRU, langEN} {
		data := samplePageData(lang)
		data.HasNetwork = true
		rec := httptest.NewRecorder()
		render(rec, "network", data)
		body := rec.Body.String()
		if err := os.WriteFile(filepath.Join(dir, "network-"+lang+".html"), rec.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		for _, match := range assets.FindAllStringSubmatch(body, -1) {
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, match[1], nil))
			path := filepath.Join(dir, match[1])
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, res.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/test-network-ui.cjs", dir)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("browser check: %v, output: %s", err, string(out))
	}
}
