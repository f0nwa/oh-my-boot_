package control

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omb/internal/images"
)

func TestImageMigrateUI(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)

	// Create admin and observer users
	if err := store.CreateUser(ctx, "adminui", "AdminPass1234!", "admin"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser(ctx, "obsui", "ObserverPass123!", "observer"); err != nil {
		t.Fatal(err)
	}

	_, adminToken, err := store.Login(ctx, "adminui", "AdminPass1234!")
	if err != nil {
		t.Fatal(err)
	}
	_, obsToken, err := store.Login(ctx, "obsui", "ObserverPass123!")
	if err != nil {
		t.Fatal(err)
	}

	mockImg := &migrationMockImages{
		fakeImagesAPI: fakeImagesAPI{
			catalog: images.Catalog{
				Storages: []images.Storage{
					{ID: 1, Name: "Локальный диск", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true, Free: 50 << 30},
					{ID: 2, Name: "Synology NFS", Kind: "nfs", Root: "/mnt/omb-nfs/nas", State: "ready", Uploads: true, Free: 100 << 30},
					{ID: 3, Name: "Архив RO", Kind: "nfs", Root: "/mnt/omb-nfs/ro", State: "ready", Uploads: false, Free: 100 << 30},
					{ID: 4, Name: "Переполненный SMB", Kind: "smb", Root: "/mnt/omb-smb/full", State: "ready", Uploads: true, Free: 100 << 20}, // 100 МБ
				},
				Images: []images.Image{
					{StorageID: 1, Path: "win11.iso", Size: 5 << 30, Present: true},
					{StorageID: 1, Path: "ubuntu.iso", Size: 3 << 30, Present: true},
				},
			},
		},
		migrations: map[string]*images.MigrationJob{
			"mig_active": {
				ID:              "mig_active",
				SourceStorageID: 1,
				Path:            "ubuntu.iso",
				TargetStorageID: 2,
				State:           "copying",
				Percent:         45,
				CopiedBytes:     1350 << 20,
				TotalBytes:      3000 << 20,
			},
		},
	}
	mockAgent := &migrationMockAgent{}

	s := &Server{
		Store:      store,
		Images:     mockImg,
		WindowsISO: mockAgent,
		UbuntuISO:  mockAgent,
	}

	// 1. Admin views /images
	reqAdmin := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqAdmin.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	recAdmin := httptest.NewRecorder()
	s.Handler().ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recAdmin.Code)
	}
	bodyAdmin := recAdmin.Body.String()

	// Check menu action present
	if !strings.Contains(bodyAdmin, "Переместить в другое хранилище…") {
		t.Fatalf("expected 'Переместить в другое хранилище…' in admin page")
	}

	// Check modal dialog rendered
	if !strings.Contains(bodyAdmin, "image-migrate-dialog") || !strings.Contains(bodyAdmin, "Перемещение образа") {
		t.Fatalf("expected image-migrate-dialog in admin page")
	}

	// Check options in target storage select
	if !strings.Contains(bodyAdmin, "Synology NFS") {
		t.Fatalf("expected Synology NFS option in select")
	}
	// Check RO option disabled
	if !strings.Contains(bodyAdmin, "Архив RO · NFS · только чтение") && !strings.Contains(bodyAdmin, "Архив RO") {
		t.Fatalf("expected Архив RO in select")
	}
	if !strings.Contains(bodyAdmin, "только чтение") {
		t.Fatalf("expected 'только чтение' note for RO storage")
	}
	// Check insufficient space option disabled
	if !strings.Contains(bodyAdmin, "недостаточно места") {
		t.Fatalf("expected 'недостаточно места' note for full storage")
	}

	// Check active migration indicator for ubuntu.iso is in "Загрузки и состояние" column, not in ISO file cell
	migRowIdx := strings.Index(bodyAdmin, `data-path="ubuntu.iso"`)
	if migRowIdx < 0 {
		t.Fatalf("expected ubuntu.iso row")
	}
	migRowEnd := strings.Index(bodyAdmin[migRowIdx:], "</tr>")
	if migRowEnd < 0 {
		t.Fatalf("expected closing </tr> for ubuntu.iso row")
	}
	migRowHTML := bodyAdmin[migRowIdx : migRowIdx+migRowEnd]
	pathCellEnd := strings.Index(migRowHTML, "</td>")
	if pathCellEnd < 0 {
		t.Fatalf("expected closing </td> for path-cell")
	}
	pathCellHTML := migRowHTML[:pathCellEnd]
	if strings.Contains(pathCellHTML, "image-migrating-status") || strings.Contains(pathCellHTML, "progress-bar-micro") {
		t.Fatalf("path-cell must not contain migration progress indicator: %s", pathCellHTML)
	}
	statusCellIdx := strings.Index(migRowHTML, `class="image-status-cell"`)
	if statusCellIdx < 0 {
		t.Fatalf("expected image-status-cell in migrating row")
	}
	statusCellHTML := migRowHTML[statusCellIdx:]
	for _, want := range []string{
		`class="status image-library-status" hidden`,
		`class="image-migrating-status"`,
		`class="status status-migrating"`,
		`class="dot dot-migrating"`,
		"Перемещается…",
		"45%",
		`class="progress-bar-micro"`,
		`width: 45%`,
	} {
		if !strings.Contains(statusCellHTML, want) {
			t.Fatalf("expected %q inside image-status-cell, got: %s", want, statusCellHTML)
		}
	}

	// Check switching state label on rebinding
	mockImg.migrations["mig_active"].State = "rebinding"
	mockImg.migrations["mig_active"].Percent = 100
	recRebind := httptest.NewRecorder()
	reqRebind := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqRebind.AddCookie(&http.Cookie{Name: "omb_session", Value: adminToken})
	s.Handler().ServeHTTP(recRebind, reqRebind)
	if recRebind.Code != http.StatusOK || !strings.Contains(recRebind.Body.String(), "Переключение…") {
		t.Fatalf("expected 'Переключение…' during rebinding")
	}

	// 2. Observer views /images
	reqObs := httptest.NewRequest(http.MethodGet, "/images", nil)
	reqObs.AddCookie(&http.Cookie{Name: "omb_session", Value: obsToken})
	recObs := httptest.NewRecorder()
	s.Handler().ServeHTTP(recObs, reqObs)

	if recObs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for observer, got %d", recObs.Code)
	}
	bodyObs := recObs.Body.String()

	// Observer must not have migrate action or migrate dialogs
	if strings.Contains(bodyObs, "data-image-migrate-open") || strings.Contains(bodyObs, "image-migrate-dialog") {
		t.Fatalf("observer page must not contain migration dialog or trigger")
	}

	// 3. Script /assets/image-migrate.js is served
	reqJS := httptest.NewRequest(http.MethodGet, "/assets/image-migrate.js", nil)
	recJS := httptest.NewRecorder()
	s.Handler().ServeHTTP(recJS, reqJS)
	if recJS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /assets/image-migrate.js, got %d", recJS.Code)
	}
	if !strings.Contains(recJS.Body.String(), "data-image-migrate-open") {
		t.Fatalf("script does not contain expected code")
	}
}
