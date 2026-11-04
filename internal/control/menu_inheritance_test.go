package control

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestMenuTitleInheritanceAndOverrideWhenRenamingDownload(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "inherits", true: "override"}[override], func(t *testing.T) {
			s, store, _ := downloadsServer(t)
			if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Original"}}); res.Code != http.StatusSeeOther {
				t.Fatalf("create %d", res.Code)
			}
			if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
				t.Fatalf("add %d", res.Code)
			}
			if override {
				if res := menuPost(t, s, "/menu/items/install_100/title", url.Values{"title": {"Special"}}); res.Code != http.StatusSeeOther {
					t.Fatalf("override %d", res.Code)
				}
			}
			if res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Renamed"}}); res.Code != http.StatusSeeOther {
				t.Fatalf("edit %d", res.Code)
			}
			want := "Renamed"
			if override {
				want = "Special"
			}
			if store.draft.Items[0].Title != want {
				t.Fatalf("title=%q want=%q", store.draft.Items[0].Title, want)
			}
		})
	}
}

func TestMenuTitleResetAndPublishedSnapshot(t *testing.T) {
	s, store, _ := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Original"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/menu/items/install_100/title", url.Values{"title": {"Special"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/menu/items/install_100/title", url.Values{"title": {"Special"}, "inherit_title": {"yes"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if !store.draft.Items[0].InheritsTitle || store.draft.Items[0].Title != "Original" {
		t.Fatalf("draft=%+v", store.draft)
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if !store.draft.Items[0].InheritsTitle || store.versions[len(store.versions)-1].Items[0].Title != "Original" {
		t.Fatalf("draft=%+v versions=%+v", store.draft, store.versions)
	}
	if res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Renamed"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	draft, active, err := s.currentDraft(context.Background())
	if err != nil || draft.Items[0].Title != "Renamed" || active.Items[0].Title != "Original" || len(diffMenuDraft(active, draft)) == 0 {
		t.Fatalf("draft=%+v active=%+v err=%v", draft, active, err)
	}
}

func TestMenuTitleOverrideSurvivesRemovalAndDownloadRename(t *testing.T) {
	s, store, _ := downloadsServer(t)
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"51"}, "title": {"Original"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/menu/items/install_100/title", url.Values{"title": {"Special"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/menu/items/install_100/remove", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/installations/100", url.Values{"boot_profile_id": {"51"}, "title": {"Renamed"}}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatal(res.Code)
	}
	if len(store.draft.Items) != 1 || store.draft.Items[0].Title != "Special" || store.draft.Items[0].InheritsTitle {
		t.Fatalf("draft=%+v", store.draft)
	}
}
