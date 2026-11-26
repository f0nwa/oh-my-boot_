package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

type hashIndexTestImages struct {
	*fakeImagesAPI
	status images.HashIndexStatus
}

func (f *hashIndexTestImages) HashIndexStatus(context.Context) (images.HashIndexStatus, error) {
	return f.status, nil
}

func TestHashIndexStatusAndScriptEndpoints(t *testing.T) {
	imgClient := &hashIndexTestImages{
		fakeImagesAPI: &fakeImagesAPI{
			settings: images.DefaultCatalogSettings(),
		},
		status: images.HashIndexStatus{
			Enabled:      true,
			RateMBps:     50,
			State:        "running",
			TotalCount:   10,
			IndexedCount: 7,
			PendingCount: 3,
			Current: &images.HashIndexCurrent{
				StorageID:   1,
				StorageName: "Local",
				Path:        "win11.iso",
				Size:        1000,
				BytesDone:   500,
				Percent:     50,
				SpeedBPS:    50 * 1024 * 1024,
			},
		},
	}
	s, _, _ := recoveryServer("observer")
	s.Images = imgClient

	// Script endpoint
	scriptRes := httptest.NewRecorder()
	s.Handler().ServeHTTP(scriptRes, httptest.NewRequest(http.MethodGet, "/assets/hash-index.js", nil))
	if scriptRes.Code != http.StatusOK || !strings.Contains(scriptRes.Body.String(), "/images/hash-index/status") {
		t.Fatalf("GET /assets/hash-index.js = %d", scriptRes.Code)
	}

	// Status endpoint (observer allowed)
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/images/hash-index/status", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /images/hash-index/status = %d, body = %s", res.Code, res.Body.String())
	}
	var got images.HashIndexStatus
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != "running" || got.IndexedCount != 7 || got.TotalCount != 10 || got.Current == nil || got.Current.Path != "win11.iso" {
		t.Fatalf("unexpected status: %+v", got)
	}
}

func TestStorageRecoverySavesHashIndexSettings(t *testing.T) {
	imgClient := &fakeImagesAPI{settings: images.DefaultCatalogSettings()}
	s, _, _ := recoveryServer("admin")
	s.Images = imgClient

	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, "/images/storage-recovery", url.Values{
		"enabled":                 {"on"},
		"interval_minutes":        {"5"},
		"rescan_enabled":          {"on"},
		"rescan_interval_minutes": {"30"},
		"hash_index_enabled":      {"on"},
		"hash_index_rate_mbps":    {"25"},
	}))
	if res.Code != http.StatusSeeOther {
		t.Fatalf("POST /images/storage-recovery = %d, body = %s", res.Code, res.Body.String())
	}
	if !imgClient.settings.HashIndexEnabled || imgClient.settings.HashIndexRateMBps != 25 {
		t.Fatalf("expected hash settings enabled=true rate=25, got %+v", imgClient.settings)
	}

	// Invalid rate -> 400
	bad := httptest.NewRecorder()
	s.Handler().ServeHTTP(bad, accountRequest(http.MethodPost, "/images/storage-recovery", url.Values{
		"enabled":                 {"on"},
		"interval_minutes":        {"5"},
		"rescan_enabled":          {"on"},
		"rescan_interval_minutes": {"30"},
		"hash_index_enabled":      {"on"},
		"hash_index_rate_mbps":    {"1500"},
	}))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for rate 1500, got %d", bad.Code)
	}
}

func TestImagesPageRendersHashBarAboveTitleAndSHAInProperties(t *testing.T) {
	sha := strings.Repeat("a", 64)
	now := time.Now()
	imgClient := &fakeImagesAPI{
		settings: images.CatalogSettings{
			RescanEnabled:         true,
			RescanIntervalMinutes: 30,
			HashIndexEnabled:      true,
			HashIndexRateMBps:     50,
		},
		catalog: images.Catalog{
			Storages: []images.Storage{{ID: 1, Name: "Local", Kind: "local", Root: "/srv/omb/iso", State: "ready", Uploads: true}},
			Images: []images.Image{
				{StorageID: 1, Path: "ready.iso", Kind: "iso", Size: 1024, Modified: now, Present: true, SHA256: sha},
				{StorageID: 1, Path: "queued.iso", Kind: "iso", Size: 2048, Modified: now, Present: true},
			},
		},
	}
	s, _, _ := recoveryServer("admin")
	s.Images = imgClient

	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/images", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /images = %d", res.Code)
	}
	body := res.Body.String()
	barIdx := strings.Index(body, `id="hash-index-bar"`)
	headIdx := strings.Index(body, `<div class="page-head">`)
	if barIdx < 0 || headIdx < 0 || barIdx > headIdx {
		t.Fatalf("expected #hash-index-bar before .page-head (bar=%d head=%d)", barIdx, headIdx)
	}
	if !strings.Contains(body, `data-sha-copy-btn="`+sha+`"`) {
		t.Fatalf("expected SHA-256 copy button for indexed ISO")
	}
	if !strings.Contains(body, "В очереди на расчёт") {
		t.Fatalf("expected queued status for unindexed ISO")
	}
}

func TestStoragesPageRendersHashIndexTaskRow(t *testing.T) {
	imgClient := &fakeImagesAPI{
		settings: images.CatalogSettings{
			RescanEnabled:         true,
			RescanIntervalMinutes: 30,
			HashIndexEnabled:      true,
			HashIndexRateMBps:     75,
		},
	}
	s, _, _ := recoveryServer("admin")
	s.Images = imgClient

	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodGet, "/storages", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("GET /storages = %d", res.Code)
	}
	body := res.Body.String()
	if !strings.Contains(body, `name="hash_index_enabled"`) || !strings.Contains(body, `name="hash_index_rate_mbps" value="75"`) || !strings.Contains(body, `id="storage-hash-index-live"`) {
		t.Fatalf("expected SHA-256 indexing controls and live status in #storage-recovery-dialog")
	}
}
