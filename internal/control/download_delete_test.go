package control

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"omb/internal/boot"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Authentication is the only Store behavior replaced; deletion, drafts and
// history run against the real PostgreSQL schema and transactions.
type downloadSessionStore struct {
	Store
	role string
}

func (s *downloadSessionStore) Session(context.Context, string) (User, error) {
	return User{ID: 1, Role: s.role, Enabled: true}, nil
}

func TestDownloadDeleteWorkflow(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	source, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{StorageID: 7, Path: "Windows.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now(), Title: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Delete me"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Keep me"})
	if err != nil {
		t.Fatal(err)
	}
	draft := MenuDraft{Title: "PXE", Items: []MenuDraftItem{{InstallationID: first.ID, Title: first.Title}, {InstallationID: other.ID, Title: other.Title}}}
	if err := store.SaveMenuDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	boot, published := fakeBoot(t)
	current := "#!ipxe\n"
	version := MenuVersion{Title: "PXE", Script: current, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(current))), Items: versionItems(draft)}
	if err := store.SaveActiveMenu(ctx, 0, version); err != nil {
		t.Fatal(err)
	}
	session := &downloadSessionStore{Store: store, role: "admin"}
	s := &Server{Store: session, Boot: boot, BootBaseURL: testBootURL}
	route := fmt.Sprintf("/installations/%d/delete", first.ID)
	for _, tc := range []struct {
		role          string
		csrf, confirm bool
		want          int
	}{
		{"operator", true, true, 403}, {"observer", true, true, 403},
		{"admin", false, true, 403}, {"admin", true, false, 400},
		{"admin", true, true, 409},
	} {
		session.role = tc.role
		form := url.Values{}
		if tc.confirm {
			form.Set("confirm", "yes")
		}
		req := accountRequest(http.MethodPost, route, form)
		if !tc.csrf {
			req = httptest.NewRequest(http.MethodPost, route, strings.NewReader("confirm=yes&csrf=bad"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
		}
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Fatalf("role=%s csrf=%v confirm=%v: status=%d want=%d body=%s", tc.role, tc.csrf, tc.confirm, res.Code, tc.want, res.Body.String())
		}
	}
	// Removing from the draft alone must not bypass the active menu guard.
	draft.Items = draft.Items[1:]
	if err := store.SaveMenuDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 409 {
		t.Fatalf("pending removal: %d", res.Code)
	}
	// Publish the removal, retaining the old version as an immutable snapshot.
	version.Script = "#!ipxe\n# removed\n"
	version.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(version.Script)))
	version.Items = versionItems(draft)
	if err := store.SaveActiveMenu(ctx, 0, version); err != nil {
		t.Fatal(err)
	}
	before, err := store.MenuVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Upgrade a populated old schema twice: history must remain unchanged.
	if _, err := store.Pool.Exec(ctx, `ALTER TABLE menu_version_items ADD CONSTRAINT menu_version_items_installation_id_fkey FOREIGN KEY (installation_id) REFERENCES installations(id) ON DELETE RESTRICT`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-03-download-deletion.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	// Fail closed if Boot has not received the saved version yet.
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 503 {
		t.Fatalf("mismatched Boot: %d", res.Code)
	}
	*published = append(*published, version.Script)
	// Delete also removes a subsequently re-added draft placement atomically.
	draft.Items = append(draft.Items, MenuDraftItem{InstallationID: first.ID, Title: first.Title})
	if err := store.SaveMenuDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 303 {
		t.Fatalf("delete: status=%d body=%s", res.Code, res.Body.String())
	}
	items, err := store.Installations(ctx)
	if err != nil || !reflect.DeepEqual(items, []Installation{other}) {
		t.Fatalf("remaining=%+v err=%v", items, err)
	}
	saved, _, err := store.MenuDraft(ctx)
	if err != nil || len(saved.Items) != 1 || saved.Items[0].InstallationID != other.ID {
		t.Fatalf("draft=%+v err=%v", saved, err)
	}
	after, err := store.MenuVersions(ctx)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("history changed: before=%+v after=%+v err=%v", before, after, err)
	}
	if _, err := store.WindowsProfile(ctx, source.ID); err != nil {
		t.Fatalf("shared source deleted: %v", err)
	}
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 404 {
		t.Fatalf("repeated delete: %d", res.Code)
	}
	if res := menuPost(t, s, "/installations/invalid/delete", url.Values{"confirm": {"yes"}}); res.Code != 400 {
		t.Fatalf("invalid id: %d", res.Code)
	}
	if err := s.verifyMenuVersion(ctx, after[1]); err == nil {
		t.Fatal("deleted entry can be restored from history")
	}
	if err := store.SaveActiveMenu(ctx, 0, after[1]); err == nil {
		t.Fatal("store published a deleted entry")
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM windows_installation_options WHERE installation_id=$1`, first.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("orphaned options=%d err=%v", count, err)
	}
}

func TestDownloadDeleteStoreGuardsActiveAndRollsBack(t *testing.T) {
	store := installationTestStore(t)
	deleter, ok := any(store).(interface {
		DeleteInstallation(context.Context, int64) error
	})
	if !ok {
		t.Fatal("store does not implement boot entry deletion")
	}
	ctx := context.Background()
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	profiles, _ := store.BootProfiles(ctx)
	item, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	draft := MenuDraft{Title: "PXE", Items: []MenuDraftItem{{InstallationID: item.ID}}}
	if err := store.SaveMenuDraft(ctx, draft); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveActiveMenu(ctx, 0, MenuVersion{Title: "PXE", Script: "#!ipxe", SHA256: "active", Items: versionItems(draft)}); err != nil {
		t.Fatal(err)
	}
	if err := deleter.DeleteInstallation(ctx, item.ID); err == nil {
		t.Fatal("deleted a published entry")
	}
	saved, _, _ := store.MenuDraft(ctx)
	if len(saved.Items) != 1 {
		t.Fatal("rejected deletion changed draft")
	}
	if err := store.SaveActiveMenu(ctx, 0, MenuVersion{Title: "PXE", Script: "#!ipxe", SHA256: "empty", Items: []MenuVersionItem{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `CREATE FUNCTION fail_download_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'deliberate failure'; END $$; CREATE TRIGGER fail_download_delete BEFORE DELETE ON installations FOR EACH ROW EXECUTE FUNCTION fail_download_delete()`); err != nil {
		t.Fatal(err)
	}
	if err := deleter.DeleteInstallation(ctx, item.ID); err == nil {
		t.Fatal("ignored database failure")
	}
	saved, _, _ = store.MenuDraft(ctx)
	if len(saved.Items) != 1 {
		t.Fatal("failed deletion removed draft placement")
	}
	if _, err := store.Pool.Exec(ctx, `DROP TRIGGER fail_download_delete ON installations`); err != nil {
		t.Fatal(err)
	}
	if err := deleter.DeleteInstallation(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := deleter.DeleteInstallation(ctx, item.ID); err != pgx.ErrNoRows {
		t.Fatalf("missing entry: %v", err)
	}
}

func TestDownloadDeleteBeforeFirstPublicationRequiresBootstrapMenu(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if err := store.EnsureBootProfiles(ctx); err != nil {
		t.Fatal(err)
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	item, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Unpublished"})
	if err != nil {
		t.Fatal(err)
	}
	client, published := fakeBoot(t)
	s := &Server{Store: &downloadSessionStore{Store: store, role: "admin"}, Boot: client, BootBaseURL: testBootURL, UbuntuReadiness: HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}}
	route := fmt.Sprintf("/installations/%d/delete", item.ID)
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 503 {
		t.Fatalf("unknown live menu allowed deletion: %d", res.Code)
	}
	initial, err := boot.Menu(testBootURL)
	if err != nil {
		t.Fatal(err)
	}
	*published = append(*published, string(initial))
	if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 303 {
		t.Fatalf("bootstrap deletion: status=%d body=%s", res.Code, res.Body.String())
	}
	items, err := store.Installations(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("entries=%+v err=%v", items, err)
	}
}

func TestDownloadListShowsSourceAndSearchesIt(t *testing.T) {
	s, store, _ := downloadsServer(t)
	store.user.Role = "admin"
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Workstation"}})
	if res.Code != 303 {
		t.Fatalf("create: %d", res.Code)
	}
	body := libraryGet(t, s, "/installations?q=Windows-new.iso")
	for _, want := range []string{"Workstation", "Windows-new.iso", "NAS"} {
		if !strings.Contains(body, want) {
			t.Fatalf("source search missing %q", want)
		}
	}
	if strings.Contains(body, `action="/installations/100/delete"`) {
		t.Fatal("entry in draft can be deleted")
	}
	if res := menuPost(t, s, "/menu/items/install_100/remove", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("remove from draft: %d", res.Code)
	}
	if body := libraryGet(t, s, "/installations"); !strings.Contains(body, `action="/installations/100/delete"`) {
		t.Fatal("entry outside menu cannot be deleted")
	}
	s.Images = offlineDownloadImages{ImagesAPI: s.Images}
	body = libraryGet(t, s, "/installations")
	if !strings.Contains(body, "Windows-new.iso") || !strings.Contains(body, "Workstation") {
		t.Fatal("catalog outage hides saved source")
	}
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"52"}, "title": {"Linux desktop"}}); res.Code != 303 {
		t.Fatalf("Ubuntu create: %d", res.Code)
	}
	if body := libraryGet(t, s, "/installations?q=ubuntu-desktop.iso"); !strings.Contains(body, "Linux desktop") || !strings.Contains(body, testBootURL+"/images/ubuntu-desktop.iso") {
		t.Fatal("system Ubuntu source is missing")
	}
	store.user.Role = "operator"
	if strings.Contains(libraryGet(t, s, "/installations"), `action="/installations/100/delete"`) {
		t.Fatal("operator sees admin deletion")
	}
	store.user.Role = "observer"
	if !strings.Contains(libraryGet(t, s, "/installations"), "Windows-new.iso") {
		t.Fatal("observer cannot see source")
	}
}
