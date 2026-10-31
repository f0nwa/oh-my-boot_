package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"omb/internal/boot"
)

// Uses Boot's real file handlers so readiness follows its configured ISO and
// prepared assets. Any GET would download data and fails the test.
func ubuntuBootResources(t *testing.T, missing string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"vmlinuz", "initrd", "bootx64.efi"} {
		if err := os.MkdirAll(filepath.Join(dir, "ubuntu"), 0755); err != nil {
			t.Fatal(err)
		}
		if name != missing {
			if err := os.WriteFile(filepath.Join(dir, "ubuntu", name), []byte("prepared resource"), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	iso := filepath.Join(t.TempDir(), "configured.iso")
	if missing != "ISO" {
		if err := os.WriteFile(iso, []byte("configured ISO"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	publisher, err := boot.NewPublisher(filepath.Join(t.TempDir(), "menu.ipxe"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := boot.NewDynamicHandlerWithISO(dir, publisher, nil, iso)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("readiness downloaded resource: %s %s", r.Method, r.URL.Path)
			http.Error(w, "HEAD required", http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// Missing any selected resource must reject publication before Boot.Publish,
// preserving the existing menu, version history and draft.
func TestUbuntuReadinessBlocksPublishAndRestoreForEachMissingResource(t *testing.T) {
	for _, missing := range []string{"ISO", "vmlinuz", "initrd", "bootx64.efi"} {
		for _, action := range []string{"publish", "rollback"} {
			t.Run(missing+"/"+action, func(t *testing.T) {
				s, store, _ := unifiedMenuFixture(t)
				s.Boot = journalBoot(t, &journalStore{})
				s.BootBaseURL = ubuntuBootResources(t, missing)
				s.UbuntuReadiness = nil
				store.versions = []MenuVersion{{ID: 9, Active: true, Title: productMenuTitle, Label: "Tools", Message: "Ready", Script: "#!ipxe\n# old menu\n", Items: []MenuVersionItem{{InstallationID: 71, Title: "Ubuntu Lab"}}}}
				before := *store.draft
				previous, err := s.Boot.CurrentMenu(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				res := menuPost(t, s, "/menu/"+action, url.Values{"id": {"9"}})
				if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), missing) {
					t.Fatalf("status=%d, expected 409 with missing resource %s", res.Code, missing)
				}
				current, err := s.Boot.CurrentMenu(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if string(current) != string(previous) || len(store.versions) != 1 || !reflect.DeepEqual(*store.draft, before) {
					t.Fatalf("readiness failure changed menu: current=%s versions=%v draft=%v", current, store.versions, store.draft)
				}
			})
		}
	}
}

// Missing resources must be visible and cannot be bypassed by posting directly.
func TestUbuntuReadinessShownInListAndFormAndBlocksCreation(t *testing.T) {
	s, store := newInstallServer(t)
	s.BootBaseURL = ubuntuBootResources(t, "initrd")
	s.UbuntuReadiness = nil
	store.installations = []Installation{{ID: 100, BootProfileID: 52, Title: "Ubuntu Lab"}}
	for _, path := range []string{"/installations?show=attention", "/installations/new?boot_profile_id=52"} {
		body := libraryGet(t, s, path)
		if !strings.Contains(body, "initrd") || !strings.Contains(body, "недоступ") {
			t.Fatalf("%s lacks unavailable initrd reason", path)
		}
		if strings.Contains(path, "/new") && !strings.Contains(body, `value="52" data-kind="ubuntu_builtin" data-windows-profile="0" data-title="Ubuntu Desktop" required disabled`) {
			t.Fatal("unavailable profile remains enabled")
		}
	}
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, "title": {"Ubuntu Recovery"}})
	if res.Code < 400 || !strings.Contains(res.Body.String(), "initrd") || len(store.installations) != 1 || store.draft != nil {
		t.Fatalf("creation status=%d installations=%v draft=%v body=%s", res.Code, store.installations, store.draft, res.Body.String())
	}
}

func TestUbuntuReadinessAllowsReadyPublication(t *testing.T) {
	s, _, published := unifiedMenuFixture(t)
	s.BootBaseURL = ubuntuBootResources(t, "")
	s.UbuntuReadiness = nil
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("status=%d published=%v body=%s", res.Code, *published, res.Body.String())
	}
}

// A typed error lets callers identify the exact shared resource; its explanation
// must remain readable in the selected interface language.
func TestUbuntuReadinessErrorIsTypedAndTranslated(t *testing.T) {
	err := (HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "initrd")}).Check(context.Background())
	var unavailable *UbuntuReadinessError
	if !errors.As(err, &unavailable) || unavailable.Resource != "initrd" {
		t.Fatalf("expected typed initrd error, got %v", err)
	}
	if translated := translate(langEN, err.Error()); !strings.Contains(translated, "Ubuntu resource initrd is unavailable") {
		t.Fatalf("untranslated error: %s", translated)
	}
}

// Readiness must fail closed for empty files and HTTP redirects, and respect
// cancellation, while never changing HEAD into GET.
func TestUbuntuReadinessRejectsEmptyRedirectAndCanceledRequests(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status, size int
	}{
		{"empty", http.StatusOK, 0}, {"redirect", http.StatusFound, 20}, {"server error", http.StatusInternalServerError, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodHead {
					t.Errorf("unexpected method %s", r.Method)
				}
				w.Header().Set("Location", "/redirect-target")
				if tc.size > 0 {
					w.Header().Set("Content-Length", "20")
				}
				w.WriteHeader(tc.status)
			}))
			defer ts.Close()
			err := (HTTPUbuntuReadiness{BaseURL: ts.URL}).Check(context.Background())
			var unavailable *UbuntuReadinessError
			if !errors.As(err, &unavailable) || unavailable.Resource != "ISO" {
				t.Fatalf("expected unavailable ISO, got %v", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}).Check(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestUbuntuReadinessDoesNotBlockWindowsOnlyMenu(t *testing.T) {
	s, store, published := unifiedMenuFixture(t)
	s.BootBaseURL = "http://127.0.0.1:1"
	s.UbuntuReadiness = nil
	store.draft.Items = []MenuDraftItem{{InstallationID: 72, Title: "Windows Lab"}}
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 {
		t.Fatalf("Windows menu blocked: %d", res.Code)
	}
}
