package control

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

// The workflow uses real Control transactions; the external services are fakes.
func TestUbuntuImageToPublishedMenuWithPostgres(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO users(id,username,password_hash,role) OVERRIDING SYSTEM VALUE VALUES(1,'operator','fixture','operator')`); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	catalog := images.Catalog{Storages: []images.Storage{{ID: 7, Name: "NAS", Kind: "smb", Root: "/mnt/omb-smb/key7", State: "ready"}}, Images: []images.Image{{StorageID: 7, Path: "ubuntu-a.iso", Size: 1234, Modified: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), Present: true}}}
	probe := &inspectUbuntuAgent{}
	boot, published := fakeBoot(t)
	server := &Server{Store: &downloadSessionStore{Store: store, role: "operator"}, Images: &fakeImagesAPI{catalog: catalog}, UbuntuISO: probe, UbuntuSource: fakeUbuntuCatalog{digest: digest}, Boot: boot, BootBaseURL: testBootURL}
	res := menuPost(t, server, "/images/iso/inspect", url.Values{"storage_id": {"7"}, "path": {"ubuntu-a.iso"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("inspect: %d %s", res.Code, res.Body.String())
	}
	profiles, err := store.BootProfiles(ctx)
	if err != nil || len(profiles) != 1 || profiles[0].UbuntuState != "verified" {
		t.Fatalf("profiles=%+v err=%v", profiles, err)
	}
	res = menuPost(t, server, "/installations", url.Values{"boot_profile_id": {fmt.Sprint(profiles[0].ID)}, "title": {"Ubuntu Lab"}, "folder": {"Linux"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", res.Code, res.Body.String())
	}
	entries, err := store.Installations(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	draft, exists, err := store.MenuDraft(ctx)
	if err != nil || exists && len(draft.Items) != 0 || len(*published) != 0 {
		t.Fatalf("creation modified menu: draft=%+v exists=%v err=%v", draft, exists, err)
	}
	profiles, err = store.BootProfiles(ctx)
	if err != nil || profiles[0].UbuntuState != "ready" {
		t.Fatalf("source not ready: %+v %v", profiles, err)
	}
	res = menuPost(t, server, fmt.Sprintf("/installations/%d/add", entries[0].ID), url.Values{})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("add: %d %s", res.Code, res.Body.String())
	}
	draft, exists, err = store.MenuDraft(ctx)
	if err != nil || !exists || len(draft.Items) != 1 || !draft.Items[0].InheritsTitle || len(*published) != 0 {
		t.Fatalf("draft=%+v exists=%v err=%v", draft, exists, err)
	}
	res = menuPost(t, server, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || len(*published) != 1 || !strings.Contains((*published)[0], fmt.Sprintf("/images/ubuntu/%d/%s.iso", profiles[0].UbuntuSourceID, digest)) {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	versions, err := store.MenuVersions(ctx)
	if err != nil || len(versions) != 1 || versions[0].Items[0].Title != "Ubuntu Lab" {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	entryPath := fmt.Sprintf("/installations/%d", entries[0].ID)
	menuPath := fmt.Sprintf("/menu/items/install_%d/title", entries[0].ID)
	if res := menuPost(t, server, entryPath, url.Values{"boot_profile_id": {fmt.Sprint(profiles[0].ID)}, "title": {"Ubuntu New"}, "folder": {"Linux"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("rename: %d %s", res.Code, res.Body.String())
	}
	draft, _, err = store.MenuDraft(ctx)
	if err != nil || draft.Items[0].Title != "Ubuntu New" || !draft.Items[0].InheritsTitle {
		t.Fatalf("inherited rename: %+v %v", draft, err)
	}
	if res := menuPost(t, server, menuPath, url.Values{"title": {"Special"}, "folder": {"Linux"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("override: %d", res.Code)
	}
	if res := menuPost(t, server, entryPath, url.Values{"boot_profile_id": {fmt.Sprint(profiles[0].ID)}, "title": {"Ubuntu Latest"}, "folder": {"Linux"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("rename after override: %d", res.Code)
	}
	draft, _, err = store.MenuDraft(ctx)
	if err != nil || draft.Items[0].Title != "Special" || draft.Items[0].InheritsTitle {
		t.Fatalf("override lost: %+v %v", draft, err)
	}
	versions, err = store.MenuVersions(ctx)
	if err != nil || versions[0].Items[0].Title != "Ubuntu Lab" {
		t.Fatalf("published snapshot changed: %+v %v", versions, err)
	}
	if res := menuPost(t, server, menuPath, url.Values{"title": {"Special"}, "folder": {"Linux"}, "inherit_title": {"yes"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("reset title: %d", res.Code)
	}
	draft, _, err = store.MenuDraft(ctx)
	if err != nil || draft.Items[0].Title != "Ubuntu Latest" || !draft.Items[0].InheritsTitle {
		t.Fatalf("inheritance not restored: %+v %v", draft, err)
	}
}
