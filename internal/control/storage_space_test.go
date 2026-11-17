package control

import (
	"strconv"
	"testing"

	"omb/internal/agent"
	"omb/internal/images"
)

func mustPctSum(t *testing.T, parts ...string) float64 {
	t.Helper()
	var sum float64
	for _, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			t.Fatalf("invalid pct %q: %v", p, err)
		}
		sum += v
	}
	return sum
}

func TestBuildStorageRowMetricsClampsNegativeOutsideAndCapsSegments(t *testing.T) {
	catalog := images.Catalog{
		Storages: []images.Storage{
			{
				ID:         1,
				Name:       "NAS Synology",
				Kind:       "smb",
				State:      "ready",
				Total:      385 << 30,
				Free:       115 << 30,
				ISOBytes:   46 << 30,
				OtherBytes: 12 << 30,
			},
			{
				ID:         2,
				Name:       "QuotaMismatch",
				Kind:       "smb",
				State:      "ready",
				Total:      100,
				Free:       80,
				ISOBytes:   25,
				OtherBytes: 5,
			},
		},
		Images: []images.Image{
			{StorageID: 1, Path: "win11.iso", Size: 46 << 30, Present: true},
			{StorageID: 1, Path: "old.iso", Size: 5 << 30, Present: false},
			{StorageID: 2, Path: "a.iso", Size: 25, Present: true},
		},
	}

	view := buildStorageSpaceView(storageSpaceInput{Catalog: catalog})
	if len(view.Storages) != 2 {
		t.Fatalf("expected 2 storages, got %d", len(view.Storages))
	}

	nas := view.Storages[0]
	if nas.ISOCount != 1 {
		t.Fatalf("nas.ISOCount = %d, want 1", nas.ISOCount)
	}
	wantOutside := int64((385 - 115 - 46 - 12) << 30)
	if nas.OutsideBytes != wantOutside {
		t.Fatalf("nas.OutsideBytes = %d, want %d", nas.OutsideBytes, wantOutside)
	}
	if sum := mustPctSum(t, nas.ISOPct, nas.OtherPct, nas.OutsidePct); sum > 100.01 {
		t.Fatalf("nas segment percentages exceed 100: %f", sum)
	}

	quota := view.Storages[1]
	if quota.OutsideBytes != 0 {
		t.Fatalf("quota.OutsideBytes = %d, want 0", quota.OutsideBytes)
	}
	if sum := mustPctSum(t, quota.ISOPct, quota.OtherPct, quota.OutsidePct); sum > 100.01 {
		t.Fatalf("quota segment percentages exceed 100: %f", sum)
	}
}

func TestBuildStorageSpaceDeduplicatesDriverWIMsAndIncludesOrphans(t *testing.T) {
	wimKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	catalog := images.Catalog{
		Storages: []images.Storage{
			{ID: 5, Name: "NAS", Kind: "smb", State: "ready", Total: 10000, Free: 5000, ISOBytes: 1000},
		},
	}
	report := &agent.DiskUsageReport{
		Filesystems: []agent.LocalFilesystem{
			{ID: "dev1", MountSample: "/srv/omb/tftp", TotalBytes: 4800, FreeBytes: 1800},
		},
		WindowsProfiles: map[int64]int64{
			17:  580,
			999: 111, // orphan on disk, missing from DB
		},
		WindowsDriverWIMs: map[string]int64{
			wimKey: 680,
		},
		WindowsDriverConfigs: map[int64]int64{
			14: 10,
			15: 10,
		},
		UbuntuSources: map[int64]int64{
			4: 412,
		},
		DriverPackagesBytes: 520,
		UEFICAUSBBytes:      440,
	}
	view := buildStorageSpaceView(storageSpaceInput{
		Catalog: catalog,
		Report:  report,
		WindowsProfiles: []WindowsProfile{
			{WindowsProfileSource: WindowsProfileSource{StorageID: 5, Path: "win11.iso", Title: "Windows 11 Pro 24H2"}, ID: 17},
		},
		DriverConfigs: []DriverConfig{
			{ID: 14, ProfileID: 17, Title: "Intel+Realtek", Build: agent.DriverBuildResult{WIMKey: wimKey}},
			{ID: 15, ProfileID: 17, Title: "Intel+Realtek B", Build: agent.DriverBuildResult{WIMKey: wimKey}},
		},
		DriverPackagesCount: 4,
		BootProfiles: []BootProfile{
			{ID: 101, Kind: BootProfileWindowsISO, Title: "Windows 11 Pro 24H2", WindowsProfileID: 17},
			{ID: 102, Kind: BootProfileUbuntuISO, Title: "Ubuntu 24.04.1 Desktop", UbuntuSourceID: 4, UbuntuStorageID: 5, UbuntuPath: "ubuntu.iso"},
		},
		Installations: []Installation{
			{ID: 21, BootProfileID: 101, Title: "Win 11 (чистая)"},
			{ID: 22, BootProfileID: 101, Title: "Win 11 (Intel+Realtek)", DriverConfigID: 14},
			{ID: 23, BootProfileID: 101, Title: "Win 11 (Отдел продаж)", DriverConfigID: 15},
			{ID: 24, BootProfileID: 102, Title: "Ubuntu 24.04 Auto"},
		},
	})

	wantTotalOMB := int64(580 + 111 + 680 + 10 + 10 + 412 + 520 + 440)
	if view.TotalOMBBytes != wantTotalOMB {
		t.Fatalf("TotalOMBBytes = %d, want %d", view.TotalOMBBytes, wantTotalOMB)
	}
	if len(view.Disks) != 1 || view.Disks[0].OMBBytes != wantTotalOMB {
		t.Fatalf("Disks = %+v", view.Disks)
	}
	// Storage 5 should sum: profile 17 (580) + unique driver WIM (680+20) + ubuntu 4 (412) = 1692
	if view.Storages[0].ServerBytes != 1692 {
		t.Fatalf("Storage[0].ServerBytes = %d, want 1692", view.Storages[0].ServerBytes)
	}

	// Verify descending sort, deduplication of wimKey (single row with 2 links), and orphan inclusion.
	var prev int64 = 1<<62 - 1
	var foundSharedWIM, foundOrphan bool
	for _, a := range view.Artifacts {
		if a.Bytes > prev {
			t.Fatalf("Artifacts not sorted descending: %+v", view.Artifacts)
		}
		prev = a.Bytes
		if a.Bytes == 700 && a.Category == artifactCategoryDrivers {
			if foundSharedWIM {
				t.Fatal("duplicate row for shared WIMKey")
			}
			foundSharedWIM = true
			if len(a.Links) != 2 || a.Links[0].Href != "/installations/22/edit" || a.Links[1].Href != "/installations/23/edit" {
				t.Fatalf("unexpected links on shared WIM row: %+v", a.Links)
			}
		}
		if a.Orphan && a.Bytes == 111 {
			foundOrphan = true
		}
	}
	if !foundSharedWIM || !foundOrphan {
		t.Fatalf("missing shared WIM (%v) or orphan (%v) in %+v", foundSharedWIM, foundOrphan, view.Artifacts)
	}
}

func TestBuildStorageSpaceDegradesWhenAgentUnavailable(t *testing.T) {
	view := buildStorageSpaceView(storageSpaceInput{
		Catalog:    images.Catalog{Storages: []images.Storage{{ID: 1, Name: "Local", Kind: "local", State: "ready", Total: 1000, Free: 500}}},
		AgentError: "Сведения о локальном диске временно недоступны",
	})
	if len(view.Storages) != 1 || view.ServerError == "" {
		t.Fatalf("unexpected degradation view: %+v", view)
	}
}

func TestSortServerArtifacts(t *testing.T) {
	rows := []serverArtifactRow{
		{Title: "Win11", StorageName: "NAS", Bytes: 600, Links: []artifactLink{{}, {}}},
		{Title: "Ubuntu", StorageName: "Local", Bytes: 200, Links: []artifactLink{{}}},
		{Title: "Astra", StorageName: "NFS", Bytes: 800},
	}

	// artifact asc
	r1 := append([]serverArtifactRow(nil), rows...)
	sortServerArtifacts(r1, "artifact", "asc")
	if r1[0].Title != "Astra" || r1[1].Title != "Ubuntu" || r1[2].Title != "Win11" {
		t.Fatalf("unexpected artifact asc sort: %+v", r1)
	}

	// artifact desc
	r2 := append([]serverArtifactRow(nil), rows...)
	sortServerArtifacts(r2, "artifact", "desc")
	if r2[0].Title != "Win11" || r2[1].Title != "Ubuntu" || r2[2].Title != "Astra" {
		t.Fatalf("unexpected artifact desc sort: %+v", r2)
	}

	// size asc
	r3 := append([]serverArtifactRow(nil), rows...)
	sortServerArtifacts(r3, "size", "asc")
	if r3[0].Bytes != 200 || r3[1].Bytes != 600 || r3[2].Bytes != 800 {
		t.Fatalf("unexpected size asc sort: %+v", r3)
	}

	// downloads desc
	r4 := append([]serverArtifactRow(nil), rows...)
	sortServerArtifacts(r4, "downloads", "desc")
	if len(r4[0].Links) != 2 || len(r4[1].Links) != 1 || len(r4[2].Links) != 0 {
		t.Fatalf("unexpected downloads desc sort: %+v", r4)
	}
}
