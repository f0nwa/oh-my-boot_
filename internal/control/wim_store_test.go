package control

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"omb/internal/agent"
	"omb/internal/images"

	"github.com/jackc/pgx/v5"
)

func TestWIMStoreSchemaConstraintsAndMigration(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)

	// Verify 0002_windows_wim_split.sql exists and is idempotent when applied on top of control.sql.
	migSQL, err := os.ReadFile("../../deploy/sql/migrations/omb_control/0002_windows_wim_split.sql")
	if err != nil {
		t.Fatalf("read migration 0002: %v", err)
	}

	// Create three windows_profiles directly before running the migration backfill:
	// 1) ready Windows ISO -> should be backfilled into windows_boot_images
	// 2) pending Windows ISO -> should NOT be backfilled
	// 3) ready Strelec ISO -> should NOT be backfilled
	now := time.Now().UTC().Truncate(time.Microsecond)
	var readyWinID, pendingWinID, readyStrelecID int64
	if err := store.Pool.QueryRow(ctx,
		`INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title,state,boot_mode)
		 VALUES(1,'win11.iso','/iso',1000,$1,'Windows 11','ready','') RETURNING id`, now).Scan(&readyWinID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx,
		`INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title,state,boot_mode)
		 VALUES(1,'win10.iso','/iso',1000,$1,'Windows 10','pending','') RETURNING id`, now).Scan(&pendingWinID); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx,
		`INSERT INTO windows_profiles(storage_id,path,storage_root,size_bytes,modified_at,title,state,boot_mode)
		 VALUES(1,'strelec.iso','/iso',1000,$1,'Strelec','ready','strelec') RETURNING id`, now).Scan(&readyStrelecID); err != nil {
		t.Fatal(err)
	}

	// Apply migration twice to verify idempotency.
	for i := 0; i < 2; i++ {
		if _, err := store.Pool.Exec(ctx, string(migSQL)); err != nil {
			t.Fatalf("apply migration pass %d: %v", i+1, err)
		}
	}

	boots, err := store.ListWindowsBootImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(boots) != 1 || boots[0].SourceKind != WIMSourceKindISO || boots[0].WindowsProfileID != readyWinID || !boots[0].HasSetupExe || boots[0].State != "ready" {
		t.Fatalf("backfilled boot images = %+v; want 1 ready ISO boot image for profile %d", boots, readyWinID)
	}
	_ = pendingWinID
	_ = readyStrelecID

	// CHECK constraint invariants on windows_boot_images.
	badBootInserts := []struct {
		name string
		sql  string
	}{
		{"iso without profile_id", `INSERT INTO windows_boot_images(source_kind,title) VALUES('iso','Bad')`},
		{"iso with storage_id", `INSERT INTO windows_boot_images(source_kind,windows_profile_id,storage_id,title) VALUES('iso',` + strconv.FormatInt(pendingWinID, 10) + `,1,'Bad')`},
		{"iso with path", `INSERT INTO windows_boot_images(source_kind,windows_profile_id,path,title) VALUES('iso',` + strconv.FormatInt(pendingWinID, 10) + `,'a.wim','Bad')`},
		{"file with profile_id", `INSERT INTO windows_boot_images(source_kind,windows_profile_id,storage_id,path,title) VALUES('file',` + strconv.FormatInt(pendingWinID, 10) + `,1,'a.wim','Bad')`},
		{"file without storage_id", `INSERT INTO windows_boot_images(source_kind,path,title) VALUES('file','a.wim','Bad')`},
		{"file with empty path", `INSERT INTO windows_boot_images(source_kind,storage_id,path,title) VALUES('file',1,'','Bad')`},
		{"invalid sha256", `INSERT INTO windows_boot_images(source_kind,storage_id,path,title,sha256) VALUES('file',1,'b.wim','Bad','not-a-sha')`},
	}
	for _, tc := range badBootInserts {
		if _, err := store.Pool.Exec(ctx, tc.sql); err == nil {
			t.Fatalf("expected CHECK failure for %s", tc.name)
		}
	}

	// Uniqueness of (storage_id, path) for source_kind='file'.
	if _, err := store.Pool.Exec(ctx,
		`INSERT INTO windows_boot_images(source_kind,storage_id,path,storage_root,size_bytes,title) VALUES('file',2,'pe/boot.wim','/iso',500,'WinPE 1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx,
		`INSERT INTO windows_boot_images(source_kind,storage_id,path,storage_root,size_bytes,title) VALUES('file',2,'pe/boot.wim','/iso',500,'WinPE 2')`); err == nil {
		t.Fatal("duplicate (storage_id, path) for source_kind='file' was accepted")
	}

	// ON DELETE RESTRICT for boot_image_id and install_image_id referenced by windows_installation_options.
	win, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		StorageID: 3, Path: "base.iso", StorageRoot: "/iso", Size: 2048, Modified: now, Title: "Base ISO",
	})
	if err != nil {
		t.Fatal(err)
	}
	var baseBootProfileID int64
	if err := store.Pool.QueryRow(ctx, `SELECT boot_profile_id FROM windows_boot_profiles WHERE windows_profile_id=$1`, win.ID).Scan(&baseBootProfileID); err != nil {
		t.Fatal(err)
	}
	fileBoot, err := store.UpsertFileBootImage(ctx, WindowsBootImageSource{
		StorageID: 3, Path: "custom-boot.wim", StorageRoot: "/iso", Size: 512, Modified: now, Title: "Custom Boot",
	})
	if err != nil {
		t.Fatal(err)
	}
	instImg, err := store.UpsertWindowsInstallImage(ctx, WindowsInstallImageSource{
		StorageID: 3, Path: "custom-install.wim", StorageRoot: "/iso", InstallFile: "install.wim", Size: 4096, Modified: now, Title: "Custom Install",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInstallation(ctx, Installation{
		BootProfileID:  baseBootProfileID,
		Title:          "Swapped ISO",
		BootImageID:    fileBoot.ID,
		InstallImageID: instImg.ID,
	})
	if err != nil {
		t.Fatalf("CreateInstallation with overrides: %v", err)
	}
	if err := store.DeleteWindowsBootImage(ctx, fileBoot.ID); err == nil {
		t.Fatal("deleted boot_image_id referenced by windows_installation_options")
	}
	if err := store.DeleteWindowsInstallImage(ctx, instImg.ID); err == nil {
		t.Fatal("deleted install_image_id referenced by windows_installation_options")
	}

	// Clearing overrides releases the references so deletion succeeds.
	created.BootImageID = 0
	created.InstallImageID = 0
	if err := store.UpdateInstallation(ctx, created); err != nil {
		t.Fatalf("clear WIM overrides: %v", err)
	}
	if err := store.DeleteWindowsBootImage(ctx, fileBoot.ID); err != nil {
		t.Fatalf("delete unreferenced boot image: %v", err)
	}
	if err := store.DeleteWindowsInstallImage(ctx, instImg.ID); err != nil {
		t.Fatalf("delete unreferenced install image: %v", err)
	}
}

func TestWIMStoreBootImageLifecycle(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	// 1. Standalone file boot.wim lifecycle.
	img, err := store.UpsertFileBootImage(ctx, WindowsBootImageSource{
		StorageID: 10, Path: "winpe/boot.wim", StorageRoot: "/srv/iso", Size: 300 << 20, Modified: now, Title: "WinPE 11",
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.ID <= 0 || img.SourceKind != WIMSourceKindFile || img.State != "pending" {
		t.Fatalf("unexpected boot image: %+v", img)
	}

	sha := strings.Repeat("a", 64)
	wimInfo := agent.WindowsWIMInfo{
		Known:  true,
		Images: []agent.WindowsWIMImage{{Index: 2, Name: "Microsoft Windows Setup (x64)", Architecture: "x64", Build: 26100}},
	}
	if err := store.SetBootImageReady(ctx, img.ID, sha, true, wimInfo); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetWindowsBootImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ready" || got.SHA256 != sha || !got.HasSetupExe || !got.WIMInfo.Known || len(got.WIMInfo.Images) != 1 {
		t.Fatalf("after SetBootImageReady: %+v", got)
	}

	if err := store.SetBootImageTitle(ctx, img.ID, "WinPE 11 24H2"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBootImageError(ctx, img.ID, "error", "corrupt wim"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetWindowsBootImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "WinPE 11 24H2" || got.State != "error" || got.Error != "corrupt wim" {
		t.Fatalf("after title/error updates: %+v", got)
	}

	if err := store.RebindWIMImageStorage(ctx, 10, "winpe/boot.wim", 20, "/mnt/nfs"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetWindowsBootImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageID != 20 || got.StorageRoot != "/mnt/nfs" {
		t.Fatalf("after RebindWIMImageStorage: %+v", got)
	}

	// 2. ISO-backed boot image auto-created when Windows ISO profile becomes ready.
	win, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		StorageID: 10, Path: "win11.iso", StorageRoot: "/srv/iso", Size: 5 << 30, Modified: now, Title: "Win11 ISO",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWindowsProfileResult(ctx, win.ID, "ready", "install.wim", ""); err != nil {
		t.Fatal(err)
	}
	// Strelec ISO becoming ready must NOT create a windows_boot_images row.
	strelec, err := store.CreateWindowsProfile(ctx, WindowsProfileSource{
		BootMode: agent.ISOStrelecMode, StorageID: 10, Path: "strelec.iso", StorageRoot: "/srv/iso", Size: 4 << 30, Modified: now, Title: "Strelec",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWindowsProfileResult(ctx, strelec.ID, "ready", "", ""); err != nil {
		t.Fatal(err)
	}

	allBoots, err := store.ListWindowsBootImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var isoBoot *WindowsBootImage
	for i := range allBoots {
		if allBoots[i].SourceKind == WIMSourceKindISO {
			if isoBoot != nil {
				t.Fatalf("expected only 1 ISO boot image, got multiple: %+v", allBoots)
			}
			isoBoot = &allBoots[i]
		}
	}
	if isoBoot == nil || isoBoot.WindowsProfileID != win.ID || isoBoot.State != "ready" || !isoBoot.HasSetupExe {
		t.Fatalf("expected ISO boot image for profile %d, got %+v", win.ID, allBoots)
	}

	// Deleting the Windows profile cascades to its ISO boot image.
	if err := store.DeleteWindowsProfile(ctx, win.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetWindowsBootImage(ctx, isoBoot.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after profile delete, got %v", err)
	}
}

func TestWIMStoreInstallImageLifecycle(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	img, err := store.UpsertWindowsInstallImage(ctx, WindowsInstallImageSource{
		StorageID: 5, Path: "images/win11-pro.esd", StorageRoot: "/srv/iso", Size: 4 << 30, Modified: now, Title: "Win11 Pro ESD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if img.ID <= 0 || img.InstallFile != "install.esd" || img.State != "pending" {
		t.Fatalf("unexpected install image: %+v", img)
	}

	// Verify linked boot_profiles(kind='windows_wim') row was created.
	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wimProfile BootProfile
	for _, p := range profiles {
		if p.Kind == BootProfileWindowsWIM && p.InstallImageID == img.ID {
			wimProfile = p
		}
	}
	if wimProfile.ID == 0 || wimProfile.Title != "Win11 Pro ESD" {
		t.Fatalf("missing linked windows_wim boot_profile: %+v", profiles)
	}

	wimInfo := agent.WindowsWIMInfo{
		Known:  true,
		Images: []agent.WindowsWIMImage{{Index: 1, Name: "Windows 11 Pro", EditionID: "Professional", Architecture: "x64", Build: 26100}},
	}
	if err := store.SetInstallImageReady(ctx, img.ID, "install.esd", wimInfo); err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstallImageTitle(ctx, img.ID, "Win11 Pro 24H2"); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetWindowsInstallImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "ready" || got.Title != "Win11 Pro 24H2" || !got.WIMInfo.Known || len(got.WIMInfo.Images) != 1 {
		t.Fatalf("after ready/title: %+v", got)
	}

	// Verify boot_profiles.title was updated too.
	profiles, err = store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range profiles {
		if p.ID == wimProfile.ID && p.Title != "Win11 Pro 24H2" {
			t.Fatalf("boot_profiles.title not updated: %+v", p)
		}
	}

	if err := store.RebindWIMImageStorage(ctx, 5, "images/win11-pro.esd", 9, "/mnt/smb"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetWindowsInstallImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageID != 9 || got.StorageRoot != "/mnt/smb" {
		t.Fatalf("after RebindWIMImageStorage: %+v", got)
	}

	if err := store.SetInstallImageError(ctx, img.ID, "error", "mount failed"); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetWindowsInstallImage(ctx, img.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "error" || got.Error != "mount failed" {
		t.Fatalf("after SetInstallImageError: %+v", got)
	}

	if err := store.DeleteWindowsInstallImage(ctx, img.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetWindowsInstallImage(ctx, img.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestWIMStoreInstallationSwapAndStandalone(t *testing.T) {
	ctx := context.Background()
	store := installationTestStore(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	bootWithSetup, err := store.UpsertFileBootImage(ctx, WindowsBootImageSource{
		StorageID: 1, Path: "boot-setup.wim", StorageRoot: "/iso", Size: 100, Modified: now, Title: "Boot With Setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBootImageReady(ctx, bootWithSetup.ID, strings.Repeat("1", 64), true, agent.WindowsWIMInfo{Known: true}); err != nil {
		t.Fatal(err)
	}

	bootWithoutSetup, err := store.UpsertFileBootImage(ctx, WindowsBootImageSource{
		StorageID: 1, Path: "boot-pe-only.wim", StorageRoot: "/iso", Size: 100, Modified: now, Title: "Boot PE Only",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBootImageReady(ctx, bootWithoutSetup.ID, strings.Repeat("2", 64), false, agent.WindowsWIMInfo{Known: true}); err != nil {
		t.Fatal(err)
	}

	bootWithSetup2, err := store.UpsertFileBootImage(ctx, WindowsBootImageSource{
		StorageID: 1, Path: "boot-setup-2.wim", StorageRoot: "/iso", Size: 100, Modified: now, Title: "Boot With Setup 2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetBootImageReady(ctx, bootWithSetup2.ID, strings.Repeat("3", 64), true, agent.WindowsWIMInfo{Known: true}); err != nil {
		t.Fatal(err)
	}

	instImg, err := store.UpsertWindowsInstallImage(ctx, WindowsInstallImageSource{
		StorageID: 1, Path: "install.wim", StorageRoot: "/iso", InstallFile: "install.wim", Size: 1000, Modified: now, Title: "Win11 Install",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetInstallImageReady(ctx, instImg.ID, "install.wim", agent.WindowsWIMInfo{Known: true}); err != nil {
		t.Fatal(err)
	}

	profiles, err := store.BootProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wimProfileID int64
	for _, p := range profiles {
		if p.Kind == BootProfileWindowsWIM && p.InstallImageID == instImg.ID {
			wimProfileID = p.ID
		}
	}
	if wimProfileID == 0 {
		t.Fatal("missing windows_wim boot_profile")
	}

	// Standalone windows_wim rejects boot.wim without setup.exe.
	if _, err := store.CreateInstallation(ctx, Installation{
		BootProfileID:  wimProfileID,
		Title:          "Bad Standalone",
		BootImageID:    bootWithoutSetup.ID,
		InstallImageID: instImg.ID,
	}); err == nil {
		t.Fatal("accepted standalone windows_wim with boot.wim lacking setup.exe")
	}

	// Standalone windows_wim succeeds with boot.wim having setup.exe.
	standalone, err := store.CreateInstallation(ctx, Installation{
		BootProfileID:  wimProfileID,
		Title:          "Win11 Standalone",
		BootImageID:    bootWithSetup.ID,
		InstallImageID: instImg.ID,
	})
	if err != nil {
		t.Fatalf("CreateInstallation standalone: %v", err)
	}

	// UpdateInstallation swaps boot_image_id cleanly.
	standalone.BootImageID = bootWithSetup2.ID
	if err := store.UpdateInstallation(ctx, standalone); err != nil {
		t.Fatalf("UpdateInstallation swap boot_image_id: %v", err)
	}

	list, err := store.Installations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].BootImageID != bootWithSetup2.ID || list[0].InstallImageID != instImg.ID {
		t.Fatalf("installations after swap = %+v", list)
	}
}

func TestWIMStoreAgentClientMethods(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "omb-wim-client-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	var gotPaths []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /windows/boot-image/prepare", func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		var req agent.WindowsBootImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != 7 {
			http.Error(w, "bad req", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(agent.WindowsBootImageResult{
			SHA256:      strings.Repeat("f", 64),
			HasSetupExe: true,
			WIMInfo:     agent.WindowsWIMInfo{Known: true},
		})
	})
	mux.HandleFunc("POST /windows/boot-image/remove", func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		var req struct{ ID int64 `json:"id"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != 7 {
			http.Error(w, "bad req", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /windows/install-image/prepare", func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		var req agent.WindowsInstallImageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != 9 {
			http.Error(w, "bad req", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(agent.WindowsInstallImageResult{
			InstallFile: "install.esd",
			WIMInfo:     agent.WindowsWIMInfo{Known: true},
		})
	})
	mux.HandleFunc("POST /windows/install-image/remove", func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		var req struct{ ID int64 `json:"id"` }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID != 9 {
			http.Error(w, "bad req", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /storages/3/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expected_kind") != "wim_or_esd" {
			http.Error(w, "missing expected_kind", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(images.UploadResult{Path: "install.wim", Kind: "wim", Size: 4})
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	ctx := context.Background()
	ac := NewAgentClient(sock)
	bootRes, err := ac.PrepareBootImage(ctx, agent.WindowsBootImageRequest{ID: 7, StorageRoot: "/iso", RelativePath: "boot.wim", Size: 10, Modified: time.Now().UTC()})
	if err != nil || !bootRes.HasSetupExe || bootRes.SHA256 != strings.Repeat("f", 64) {
		t.Fatalf("PrepareBootImage = %+v, err = %v", bootRes, err)
	}
	if err := ac.RemoveBootImage(ctx, 7); err != nil {
		t.Fatalf("RemoveBootImage: %v", err)
	}
	instRes, err := ac.PrepareInstallImage(ctx, agent.WindowsInstallImageRequest{ID: 9, StorageRoot: "/iso", RelativePath: "install.esd", Size: 20, Modified: time.Now().UTC()})
	if err != nil || instRes.InstallFile != "install.esd" {
		t.Fatalf("PrepareInstallImage = %+v, err = %v", instRes, err)
	}
	if err := ac.RemoveInstallImage(ctx, 9); err != nil {
		t.Fatalf("RemoveInstallImage: %v", err)
	}

	ic := NewImagesClient(sock)
	upRes, err := ic.UploadImage(ctx, 3, "", "install.wim", "wim_or_esd", strings.NewReader("data"), 4)
	if err != nil || upRes.Kind != "wim" {
		t.Fatalf("UploadImage = %+v, err = %v", upRes, err)
	}
	if len(gotPaths) != 4 {
		t.Fatalf("agent calls = %v, want 4", gotPaths)
	}
}
