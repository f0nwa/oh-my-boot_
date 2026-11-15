package control

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInspectionMatchesOnlyCurrentCatalogFile(t *testing.T) {
	source := WindowsProfileSource{StorageID: 7, Path: "nested/arm64-ubuntu-Strelec.iso", StorageRoot: "/mnt/omb-smb/key7", Size: 1234, Modified: time.Now().Truncate(time.Microsecond)}
	checked := inspectionRecord(source, "windows")
	if !checked.Matches(source) {
		t.Fatal("fresh result not accepted")
	}
	for _, change := range []func(*WindowsProfileSource){func(s *WindowsProfileSource) { s.Size++ }, func(s *WindowsProfileSource) { s.Modified = s.Modified.Add(time.Second) }, func(s *WindowsProfileSource) { s.StorageRoot += "-new" }, func(s *WindowsProfileSource) { s.StorageID++ }, func(s *WindowsProfileSource) { s.Path = "other.iso" }} {
		other := source
		change(&other)
		if checked.Matches(other) {
			t.Fatalf("stale result accepted: %+v", other)
		}
	}
}

func TestUninspectedRawISORequiresContentInspection(t *testing.T) {
	s, store, prep := downloadsServer(t)
	store.inspections = nil
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Windows-new.iso"}, "title": {"Unchecked"}})
	if res.Code != http.StatusBadRequest || len(store.installations) != 0 || prep.calls != 0 {
		t.Fatalf("unchecked ISO created: status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestRenamedStrelecUsesPersistedContentType(t *testing.T) {
	s, store, prep := downloadsServer(t)
	setTestInspection(s, store, "ordinary.iso", "strelec")
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:ordinary.iso"}, "title": {"Service"}})
	if res.Code != http.StatusSeeOther || prep.req.BootMode != "strelec" || store.bootProfiles[len(store.bootProfiles)-1].Kind != BootProfileWinPEISO {
		t.Fatalf("wrong mode: status=%d req=%+v body=%s", res.Code, prep.req, res.Body.String())
	}
}

func TestImageInspectionStoreAndMigration(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	source := WindowsProfileSource{StorageID: 7, Path: "nested/test.iso", StorageRoot: "/iso", Size: 1234, Modified: time.Now().Truncate(time.Microsecond)}
	if err := store.SetImageInspection(ctx, inspectionRecord(source, "windows")); err != nil {
		t.Fatal(err)
	}
	got, err := store.ImageInspections(ctx)
	if err != nil || !got[imageAuthorKey(7, source.Path)].Matches(source) || got[imageAuthorKey(7, source.Path)].Kind != "windows" {
		t.Fatalf("records=%v err=%v", got, err)
	}
	source.Size++
	if err := store.SetImageInspection(ctx, inspectionRecord(source, "unsupported")); err != nil {
		t.Fatal(err)
	}
	got, err = store.ImageInspections(ctx)
	if err != nil || got[imageAuthorKey(7, source.Path)].Kind != "unsupported" || len(got) != 1 {
		t.Fatalf("upsert: %v %v", got, err)
	}
}

func TestLibraryDoesNotInferTypeFromFilename(t *testing.T) {
	s, store, _ := downloadsServer(t)
	setTestInspection(s, store, "ubuntu-arm64-strelec.iso", "windows")
	page := libraryGet(t, s, "/images")
	if strings.Contains(page, "Тип по имени файла") || !strings.Contains(page, "<th>Образ</th>") || !strings.Contains(page, "<td>Windows<br>") {
		t.Fatal("library still guesses the type from a name")
	}
}

func (s *newInstallStore) ImageInspections(context.Context) (map[string]ImageInspection, error) {
	return s.inspections, nil
}
func (s *newInstallStore) SetImageInspection(_ context.Context, record ImageInspection) error {
	if s.inspections == nil {
		s.inspections = map[string]ImageInspection{}
	}
	s.inspections[imageAuthorKey(record.StorageID, record.Path)] = record
	return nil
}
func setTestInspection(s *Server, store *downloadsStore, path, kind string) {
	catalog := s.Images.(*fakeImagesAPI)
	catalog.catalog.Images[1].Path = path
	image := catalog.catalog.Images[1]
	for _, storage := range catalog.catalog.Storages {
		if storage.ID == image.StorageID {
			_ = store.SetImageInspection(context.Background(), inspectionRecord(WindowsProfileSource{StorageID: image.StorageID, Path: path, StorageRoot: storage.Root, Size: image.Size, Modified: image.Modified}, kind))
			return
		}
	}
	panic("test storage missing")
}

func TestInspectionPersistsContentResultAndInvalidatesAfterReplacement(t *testing.T) {
	for _, tc := range []struct {
		kind, label string
		fail        bool
		status      int
	}{
		{"windows", "Windows", false, http.StatusSeeOther},
		{"strelec", "WinPE Strelec", false, http.StatusSeeOther},
		{"unsupported", "Не поддерживается", false, http.StatusUnprocessableEntity},
		{"windows", "Не проверен", true, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.label+tc.kind, func(t *testing.T) {
			s, store, _ := downloadsServer(t)
			api := s.Images.(*fakeImagesAPI)
			api.catalog.Images[1].Path = "ubuntu-arm64-renamed.iso"
			probe := &genericISOAgent{kind: tc.kind}
			if tc.fail {
				probe.err = context.DeadlineExceeded
			}
			s.UbuntuISO = probe
			res := menuPost(t, s, "/images/iso/inspect", url.Values{"storage_id": {"7"}, "path": {"ubuntu-arm64-renamed.iso"}})
			if res.Code != tc.status {
				t.Fatalf("inspect=%d body=%s", res.Code, res.Body.String())
			}
			record, ok := store.inspections[imageAuthorKey(7, "ubuntu-arm64-renamed.iso")]
			want := tc.kind
			if tc.fail {
				want = "error"
			}
			if !ok || record.Kind != want {
				t.Fatalf("record=%+v", record)
			}
			page := libraryGet(t, s, "/images")
			wantCell := "<td>" + tc.label + "</td>"
			if tc.label == "Windows" || tc.label == "WinPE Strelec" {
				wantCell = "<td>" + tc.label + "<br>"
			}
			if !strings.Contains(page, wantCell) {
				t.Fatalf("missing type %s", tc.label)
			}
			api.catalog.Images[1].Size++
			page = libraryGet(t, s, "/images")
			if tc.kind != "windows" || !tc.fail {
				if strings.Contains(page, wantCell) {
					t.Fatal("stale inspection is shown for replaced ISO")
				}
			}
		})
	}
}

func TestInspectThenCreateRenamedStrelec(t *testing.T) {
	s, store, prep := downloadsServer(t)
	s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "service.iso"
	s.UbuntuISO = &genericISOAgent{kind: "strelec"}
	res := menuPost(t, s, "/images/iso/inspect", url.Values{"storage_id": {"7"}, "path": {"service.iso"}})
	if res.Code != http.StatusSeeOther || store.sourceCreates != 0 {
		t.Fatalf("inspect=%d creates=%d", res.Code, store.sourceCreates)
	}
	page := libraryGet(t, s, res.Header().Get("Location"))
	if !strings.Contains(page, `data-kind="winpe_iso"`) {
		t.Fatal("form lost detected service type")
	}
	res = menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:service.iso"}, "title": {"Service"}})
	if res.Code != http.StatusSeeOther || prep.req.BootMode != "strelec" {
		t.Fatalf("create=%d request=%+v", res.Code, prep.req)
	}
}

func TestImageInspectionMigrationTwice(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, "DROP TABLE image_inspections"); err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../deploy/sql/2026-11-05-verified-image-types.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.Pool.Exec(ctx, string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetImageInspection(ctx, inspectionRecord(WindowsProfileSource{StorageID: 7, Path: "nested/a.iso", StorageRoot: "/iso", Size: 100, Modified: time.Now()}, "windows")); err != nil {
		t.Fatal(err)
	}
}
