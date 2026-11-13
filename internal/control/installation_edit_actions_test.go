package control

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"omb/internal/boot"
	"reflect"
	"strings"
	"testing"
)

type rejectEditPublication struct{ transport http.RoundTripper }

func (p rejectEditPublication) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Path == "/publish" {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("publication rejected")), Header: make(http.Header)}, nil
	}
	return p.transport.RoundTrip(r)
}

func TestInstallationRemoveDeleteLastOrDraftOnly(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprintf("published=%v", published), func(t *testing.T) {
			ctx := context.Background()
			store := installationTestStore(t)
			if _, err := store.Pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'admin','fixture','admin')`); err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureBootProfiles(ctx); err != nil {
				t.Fatal(err)
			}
			profiles, _ := store.BootProfiles(ctx)
			entry, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: "Only entry"})
			if err != nil {
				t.Fatal(err)
			}
			client, scripts := fakeBoot(t)
			initial, err := boot.Menu(testBootURL)
			if err != nil {
				t.Fatal(err)
			}
			*scripts = append(*scripts, string(initial))
			s := &Server{Store: &downloadSessionStore{Store: store, role: "admin"}, Boot: client, BootBaseURL: testBootURL, UbuntuReadiness: HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}}
			draft := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Keep settings", NoDiagnostics: true, LocalTimeout: 37, NoLocalTimer: true, DefaultInstallationID: entry.ID, Items: []MenuDraftItem{{InstallationID: entry.ID, Title: entry.Title}}}
			if err := store.SaveMenuDraft(ctx, draft); err != nil {
				t.Fatal(err)
			}
			if published {
				if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != 303 {
					t.Fatalf("publish: %d", res.Code)
				}
			}
			before := len(*scripts)
			if res := menuPost(t, s, fmt.Sprintf("/installations/%d/remove-delete", entry.ID), url.Values{"confirm": {"yes"}}); res.Code != 303 {
				t.Fatalf("remove-delete: %d %s", res.Code, res.Body.String())
			}
			items, _ := store.Installations(ctx)
			saved, _, _ := store.MenuDraft(ctx)
			if len(items) != 0 || len(saved.Items) != 0 || saved.DefaultInstallationID != 0 || saved.Message != "Keep settings" {
				t.Fatalf("entries=%+v draft=%+v", items, saved)
			}
			if published {
				if len(*scripts) != before+1 {
					t.Fatal("last item removal was not published")
				}
				versions, _ := store.MenuVersions(ctx)
				for _, v := range versions {
					if v.Active && (len(v.Items) != 0 || v.DefaultInstallationID != 0 || !v.NoDiagnostics || !v.NoLocalTimer || v.LocalTimeout != 37) {
						t.Fatalf("empty menu lost settings: %+v", v)
					}
				}
			} else if len(*scripts) != before {
				t.Fatal("draft-only deletion changed Boot")
			}
		})
	}
}

// Catches publishing unrelated draft changes or deleting before Boot accepts
// the replacement menu. Only authentication and the external Boot are faked.
func TestInstallationRemoveAndDelete(t *testing.T) {
	for _, pendingRemoval := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending-removal=%v", pendingRemoval), func(t *testing.T) {
			ctx := context.Background()
			store := installationTestStore(t)
			if _, err := store.Pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'admin','fixture','admin')`); err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureBootProfiles(ctx); err != nil {
				t.Fatal(err)
			}
			profiles, _ := store.BootProfiles(ctx)
			var entries []Installation
			for _, title := range []string{"Remove me", "Published title", "Draft only"} {
				item, err := store.CreateInstallation(ctx, Installation{BootProfileID: profiles[0].ID, Title: title})
				if err != nil {
					t.Fatal(err)
				}
				entries = append(entries, item)
			}
			boot, _ := fakeBoot(t)
			session := &downloadSessionStore{Store: store, role: "admin"}
			s := &Server{Store: session, Boot: boot, BootBaseURL: testBootURL, UbuntuReadiness: HTTPUbuntuReadiness{BaseURL: ubuntuBootResources(t, "")}}
			active := MenuDraft{Title: productMenuTitle, Label: "Diagnostics", Message: "Published message", DefaultInstallationID: entries[0].ID, Items: []MenuDraftItem{
				{InstallationID: entries[0].ID, Title: "Remove me"}, {InstallationID: entries[1].ID, Title: "Published title"},
			}}
			if err := store.SaveMenuDraft(ctx, active); err != nil {
				t.Fatal(err)
			}
			if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != 303 {
				t.Fatalf("publish: %d %s", res.Code, res.Body.String())
			}
			draft := active
			draft.Message, draft.DefaultInstallationID = "Unpublished message", entries[2].ID
			draft.Items = []MenuDraftItem{{InstallationID: entries[2].ID, Title: "Draft only"}, {InstallationID: entries[1].ID, Title: "Unpublished title", Folder: "Draft folder"}}
			if !pendingRemoval {
				draft.Items = append(draft.Items, active.Items[0])
			}
			if err := store.SaveMenuDraft(ctx, draft); err != nil {
				t.Fatal(err)
			}
			before, _, _ := store.MenuDraft(ctx)
			route := fmt.Sprintf("/installations/%d/remove-delete", entries[0].ID)
			for _, role := range []string{"operator", "observer"} {
				session.role = role
				if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 403 {
					t.Fatalf("role=%s: %d", role, res.Code)
				}
			}
			session.role = "admin"
			if res := menuPost(t, s, route, url.Values{}); res.Code != 400 {
				t.Fatalf("confirmation: %d", res.Code)
			}
			req := httptest.NewRequest(http.MethodPost, route, strings.NewReader("confirm=yes&csrf=invalid"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
			res := httptest.NewRecorder()
			s.Handler().ServeHTTP(res, req)
			if res.Code != 403 {
				t.Fatalf("csrf: %d", res.Code)
			}
			// An unreachable Boot must preserve both the entry and the draft.
			s.Boot = NewBootClient("/tmp/omb-missing-edit-actions.sock")
			if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 503 {
				t.Fatalf("offline: %d", res.Code)
			}
			items, _ := store.Installations(ctx)
			saved, _, _ := store.MenuDraft(ctx)
			if len(items) != 3 || !reflect.DeepEqual(before, saved) {
				t.Fatal("failed publication changed saved data")
			}
			s.Boot = boot
			// Boot can be readable while refusing publication. Do not remove the
			// saved entry or its draft placement when that request fails.
			s.Boot = &BootClient{http: &http.Client{Transport: rejectEditPublication{transport: boot.http.Transport}}}
			if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 503 {
				t.Fatalf("publication rejected: %d", res.Code)
			}
			items, _ = store.Installations(ctx)
			saved, _, _ = store.MenuDraft(ctx)
			if len(items) != 3 || !reflect.DeepEqual(before, saved) {
				t.Fatal("rejected publication changed saved data")
			}
			s.Boot = boot
			// A failed version write must restore the previous live menu.
			previous, _ := boot.CurrentMenu(ctx)
			if _, err := store.Pool.Exec(ctx, `CREATE FUNCTION reject_edit_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'version write failed'; END $$; CREATE TRIGGER reject_edit_version BEFORE INSERT ON menu_versions FOR EACH ROW EXECUTE FUNCTION reject_edit_version()`); err != nil {
				t.Fatal(err)
			}
			if res := menuPost(t, s, route, url.Values{"confirm": {"yes"}}); res.Code != 500 {
				t.Fatalf("version write failed: %d", res.Code)
			}
			live, _ := boot.CurrentMenu(ctx)
			items, _ = store.Installations(ctx)
			saved, _, _ = store.MenuDraft(ctx)
			if string(live) != string(previous) || len(items) != 3 || !reflect.DeepEqual(before, saved) {
				t.Fatal("failed version write did not restore menu/data")
			}
			if _, err := store.Pool.Exec(ctx, `DROP TRIGGER reject_edit_version ON menu_versions`); err != nil {
				t.Fatal(err)
			}
			res = menuPost(t, s, route, url.Values{"confirm": {"yes"}})
			if res.Code != 303 || !strings.HasPrefix(res.Header().Get("Location"), "/installations?") {
				t.Fatalf("delete: %d %s location=%s", res.Code, res.Body.String(), res.Header().Get("Location"))
			}
			items, _ = store.Installations(ctx)
			if len(items) != 2 || items[0].ID != entries[1].ID || items[1].ID != entries[2].ID {
				t.Fatalf("remaining=%+v", items)
			}
			versions, _ := store.MenuVersions(ctx)
			if len(versions) != 2 {
				t.Fatalf("versions=%d", len(versions))
			}
			for _, v := range versions {
				if v.Active {
					if v.DefaultInstallationID != 0 || v.Message != "Published message" || len(v.Items) != 1 || v.Items[0].InstallationID != entries[1].ID || v.Items[0].Title != "Published title" || v.Items[0].Folder != "" {
						t.Fatalf("unrelated changes published: %+v", v)
					}
					live, _ := boot.CurrentMenu(ctx)
					if string(live) != v.Script || strings.Contains(string(live), "Remove me") || strings.Contains(string(live), "Draft only") {
						t.Fatal("Boot menu does not match the targeted removal")
					}
				}
			}
			saved, _, _ = store.MenuDraft(ctx)
			if len(saved.Items) != 2 || saved.Items[0].InstallationID != entries[2].ID || saved.Items[1].Title != "Unpublished title" || saved.Message != "Unpublished message" || saved.DefaultInstallationID != entries[2].ID {
				t.Fatalf("lost draft: %+v", saved)
			}
			if profiles, _ := store.BootProfiles(ctx); len(profiles) != 1 {
				t.Fatal("shared source removed")
			}
		})
	}
}

// Catches missing editor actions or admin-only deletion exposed to operators.
func TestInstallationEditorActions(t *testing.T) {
	s, store := newInstallServer(t)
	store.user.Role = "admin"
	store.installations = []Installation{{ID: 100, BootProfileID: 50, Title: "Test entry"}}
	for _, tc := range []struct {
		name                       string
		draft, active              bool
		role                       string
		remove, combined, deletion bool
	}{
		{"free", false, false, "admin", false, false, true},
		{"published", true, true, "admin", true, true, false},
		{"draft-only", true, false, "admin", true, true, false},
		{"pending-removal", false, true, "admin", false, true, false},
		{"operator", true, true, "operator", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.user.Role = tc.role
			store.draft = &MenuDraft{Title: productMenuTitle}
			store.versions = nil
			placement := MenuDraftItem{InstallationID: 100, Title: "Test entry"}
			if tc.draft {
				store.draft.Items = []MenuDraftItem{placement}
			}
			if tc.active {
				store.versions = []MenuVersion{{Active: true, Items: []MenuVersionItem{{InstallationID: 100, Title: "Test entry"}}}}
			}
			body := libraryGet(t, s, "/installations/100/edit")
			for _, check := range []struct {
				text string
				want bool
			}{
				{`action="/menu/items/install_100/remove"`, tc.remove},
				{`action="/installations/100/remove-delete"`, tc.combined},
				{`action="/installations/100/delete"`, tc.deletion},
			} {
				if strings.Contains(body, check.text) != check.want {
					t.Errorf("%s present=%v want=%v", check.text, !check.want, check.want)
				}
			}
		})
	}
	store.user.Role = "operator"
	store.draft = &MenuDraft{Items: []MenuDraftItem{{InstallationID: 100, Title: "Test entry"}}}
	res := menuPost(t, s, "/menu/items/install_100/remove", url.Values{"return": {"/installations/100/edit"}})
	if res.Code != 303 || !strings.HasPrefix(res.Header().Get("Location"), "/installations/100/edit?") || len(store.draft.Items) != 0 || len(store.installations) != 1 {
		t.Fatalf("remove from editor: %d location=%s", res.Code, res.Header().Get("Location"))
	}
}
