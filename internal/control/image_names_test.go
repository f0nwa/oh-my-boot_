package control

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
)

type namedImagesStore struct {
	*downloadsStore
	names map[string]string
}

func (s *namedImagesStore) ImageNames(context.Context) (map[string]string, error) {
	return s.names, nil
}
func (s *namedImagesStore) SetImageName(_ context.Context, id int64, path, name string) error {
	s.names[imageAuthorKey(id, path)] = name
	return nil
}

func TestImageNamesAreSharedAcrossLibraryAndDownloads(t *testing.T) {
	s, base, _ := downloadsServer(t)
	store := &namedImagesStore{downloadsStore: base, names: map[string]string{}}
	s.Store = store
	res := menuPost(t, s, "/images/name", url.Values{"storage_id": {"7"}, "path": {"Windows-new.iso"}, "name": {"Windows 10 Pro 22H2"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("name status=%d body=%s", res.Code, res.Body.String())
	}
	library := libraryGet(t, s, "/images")
	if !strings.Contains(library, "Windows 10 Pro 22H2") || !strings.Contains(library, "Windows-new.iso") {
		t.Fatal("library must show both friendly name and original filename")
	}
	form := libraryGet(t, s, "/installations/new?storage_id=7&path=Windows-new.iso")
	if !strings.Contains(form, "Windows 10 Pro 22H2") || !strings.Contains(form, "Windows-new.iso") {
		t.Fatal("new download form lacks friendly name and file")
	}
}

func TestImageNameRejectsUnknownFileAndObserver(t *testing.T) {
	s, base, _ := downloadsServer(t)
	store := &namedImagesStore{downloadsStore: base, names: map[string]string{}}
	s.Store = store
	form := url.Values{"storage_id": {"7"}, "path": {"missing.iso"}, "name": {"Missing"}}
	if got := menuPost(t, s, "/images/name", form); got.Code != http.StatusBadRequest || len(store.names) != 0 {
		t.Fatalf("unknown image: status=%d", got.Code)
	}
	base.user.Role = "observer"
	form.Set("path", "Windows-new.iso")
	if got := menuPost(t, s, "/images/name", form); got.Code != http.StatusForbidden || len(store.names) != 0 {
		t.Fatalf("observer: status=%d", got.Code)
	}
}

func TestNamedImageShownInMultipleDownloadsAndSeparateStorage(t *testing.T) {
	s, base, _ := downloadsServer(t)
	s.Store = &namedImagesStore{downloadsStore: base, names: map[string]string{imageAuthorKey(7, "images/windows.iso"): "Windows Lab"}}
	profiles := []WindowsProfile{
		{ID: 1, WindowsProfileSource: WindowsProfileSource{StorageID: 7, Path: "images/windows.iso", Title: "Windows x64"}, State: "ready", InstallFile: "install.wim"},
		{ID: 2, WindowsProfileSource: WindowsProfileSource{StorageID: 8, Path: "images/windows.iso", Title: "Windows x64"}, State: "ready", InstallFile: "install.wim"},
	}
	sources := InstallationMenuSources{BootProfiles: []BootProfile{{ID: 11, Kind: BootProfileWindowsISO, WindowsProfileID: 1}, {ID: 12, Kind: BootProfileWindowsISO, WindowsProfileID: 2}}, Installations: []Installation{{ID: 21, BootProfileID: 11, Title: "First"}, {ID: 22, BootProfileID: 11, Title: "Second"}, {ID: 23, BootProfileID: 12, Title: "Other storage"}}}
	rows := s.installationRows(context.Background(), profiles, nil, nil, MenuDraft{}, MenuDraft{}, sources)
	got := map[int64]string{}
	for _, row := range rows {
		got[row.ID] = row.Image
	}
	if got[21] != "Windows Lab" || got[22] != "Windows Lab" || got[23] != "windows.iso" {
		t.Fatalf("incorrect image titles: %v", got)
	}
}

func TestImageNameValidation(t *testing.T) {
	for _, name := range []string{"", "  ", "bad\nname", strings.Repeat("я", 121)} {
		s, base, _ := downloadsServer(t)
		store := &namedImagesStore{downloadsStore: base, names: map[string]string{}}
		s.Store = store
		got := menuPost(t, s, "/images/name", url.Values{"storage_id": {"7"}, "path": {"Windows-new.iso"}, "name": {name}})
		if got.Code != http.StatusBadRequest || len(store.names) != 0 {
			t.Fatalf("name %q: status=%d", name, got.Code)
		}
	}
}

func TestImageNamesPersistByStorageAndPath(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	if err := store.SetImageName(ctx, 7, "nested/win.iso", "Первое имя"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageName(ctx, 8, "nested/win.iso", "Другой ISO"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetImageName(ctx, 7, "nested/win.iso", "Новое имя"); err != nil {
		t.Fatal(err)
	}
	names, err := store.ImageNames(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if names[imageAuthorKey(7, "nested/win.iso")] != "Новое имя" || names[imageAuthorKey(8, "nested/win.iso")] != "Другой ISO" {
		t.Fatalf("names=%v", names)
	}
}

func TestImageNamesMigrationWorksTwice(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	if _, err := store.Pool.Exec(ctx, "DROP TABLE image_names"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/control.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetImageName(ctx, 6, "folder/test.iso", "Test ISO"); err != nil {
		t.Fatal(err)
	}
	names, err := store.ImageNames(ctx)
	if err != nil || names[imageAuthorKey(6, "folder/test.iso")] != "Test ISO" {
		t.Fatalf("names=%v err=%v", names, err)
	}
}

func TestImageNameOpensImageDetails(t *testing.T) {
	s, base, _ := downloadsServer(t)
	store := &namedImagesStore{downloadsStore: base, names: map[string]string{imageAuthorKey(7, "Windows-new.iso"): "Windows 10"}}
	s.Store = store
	page := libraryGet(t, s, "/images")
	if !strings.Contains(page, `data-image-dialog=`) || !strings.Contains(page, `>Windows 10</button>`) || !strings.Contains(page, `<dialog`) || !strings.Contains(page, `action="/images/name"`) || !strings.Contains(page, `Windows-new.iso`) {
		t.Fatal("named ISO must open a dialog with a rename form and original filename")
	}
	if strings.Contains(page, "Изменить название образа</summary>") || strings.Contains(page, "Указать название образа</summary>") {
		t.Fatal("rename control still expands the table row")
	}
	base.user.Role = "observer"
	page = libraryGet(t, s, "/images")
	if !strings.Contains(page, `data-image-dialog=`) || strings.Contains(page, `action="/images/name"`) {
		t.Fatal("observers should see image details without rename controls")
	}
}
