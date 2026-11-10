package control

import (
	"context"
	"errors"
	"strings"
	"testing"

	"omb/internal/agent"
	"omb/internal/images"
	"omb/internal/ueficausb"
)

// These probes sit at the database/service boundary. An unrelated failed
// source must neither delay a page nor suppress the data it can still show.
type libraryProbeStore struct {
	*draftStore
	calls        map[string]int
	unrelatedErr error
}

func (s *libraryProbeStore) WindowsProfiles(ctx context.Context) ([]WindowsProfile, error) {
	s.calls["profiles"]++
	return s.draftStore.WindowsProfiles(ctx)
}
func (s *libraryProbeStore) DriverConfigs(ctx context.Context) ([]DriverConfig, error) {
	s.calls["driver configs"]++
	return s.draftStore.DriverConfigs(ctx)
}
func (s *libraryProbeStore) Installations(ctx context.Context) ([]Installation, error) {
	s.calls["installations"]++
	return s.draftStore.Installations(ctx)
}
func (s *libraryProbeStore) BootProfiles(ctx context.Context) ([]BootProfile, error) {
	s.calls["boot profiles"]++
	return s.draftStore.BootProfiles(ctx)
}

func (s *libraryProbeStore) WindowsAnswers(ctx context.Context) ([]WindowsAnswerVariant, error) {
	s.calls["answers"]++
	if s.unrelatedErr != nil {
		return nil, s.unrelatedErr
	}
	return s.draftStore.WindowsAnswers(ctx)
}
func (s *libraryProbeStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	s.calls["autoinstalls"]++
	return nil, s.unrelatedErr
}
func (s *libraryProbeStore) ResourceAuthors(context.Context) (ResourceAuthors, error) {
	s.calls["authors"]++
	return ResourceAuthors{Answers: map[int64]string{s.answers[0].ID: "answer-author"}}, s.unrelatedErr
}
func (s *libraryProbeStore) ImageNames(context.Context) (map[string]string, error) {
	s.calls["image names"]++
	return nil, s.unrelatedErr
}
func (*libraryProbeStore) SetImageName(context.Context, int64, string, string) error { return nil }
func (s *libraryProbeStore) ImageInspections(context.Context) (map[string]ImageInspection, error) {
	s.calls["image inspections"]++
	return nil, s.unrelatedErr
}
func (*libraryProbeStore) SetImageInspection(context.Context, ImageInspection) error { return nil }

type libraryProbeImages struct {
	fakeImagesAPI
	calls int
}

func (s *libraryProbeImages) Catalog(ctx context.Context) (images.Catalog, error) {
	s.calls++
	return s.fakeImagesAPI.Catalog(ctx)
}

type libraryProbeRecovery struct {
	fakeRecoveryAPI
	calls int
}

func (s *libraryProbeRecovery) StorageRecoveryStatus(ctx context.Context) (agent.RecoveryStatus, error) {
	s.calls++
	return s.fakeRecoveryAPI.StorageRecoveryStatus(ctx)
}

func TestAnswersLibraryIndependentOfImagesAndRecovery(t *testing.T) {
	for _, role := range []string{"observer", "operator", "admin"} {
		t.Run(role, func(t *testing.T) {
			store := &libraryProbeStore{draftStore: newDraftStore(t, role, []MenuDraftItem{{InstallationID: 2031}, {InstallationID: 3002}}), calls: map[string]int{}}
			api := &libraryProbeImages{fakeImagesAPI: fakeImagesAPI{err: errors.New("Images offline")}}
			recovery := &libraryProbeRecovery{fakeRecoveryAPI: fakeRecoveryAPI{err: errors.New("Agent offline")}}
			s := &Server{Store: store, Images: api, StorageRecovery: recovery}
			body := libraryGet(t, s, "/answers")
			for _, want := range []string{store.answers[0].Title, store.profiles[0].Title, "answer-author", "В меню: 2"} {
				if !strings.Contains(body, want) {
					t.Errorf("answers missing %q", want)
				}
			}
			if api.calls != 0 || recovery.calls != 0 {
				t.Errorf("answers waited on unrelated services: Images=%d recovery=%d", api.calls, recovery.calls)
			}
			if store.calls["autoinstalls"] != 1 {
				t.Errorf("autoinstall list read %d times, want once", store.calls["autoinstalls"])
			}
			if store.calls["image names"] != 0 || store.calls["image inspections"] != 0 {
				t.Errorf("answers queried ISO metadata: %v", store.calls)
			}
		})
	}
}

func TestLibraryPagesSkipUnrelatedAnswerAndMetadataFailures(t *testing.T) {
	for _, path := range []string{"/storages", "/system", "/usb", "/"} {
		t.Run(path, func(t *testing.T) {
			base := newDraftStore(t, "observer", nil)
			if path == "/" {
				base.profiles[0].State, base.profiles[0].Error = "unavailable", "profile unavailable"
				base.configs[0].State, base.configs[0].Error = "error", "WinPE build failed"
			}
			store := &libraryProbeStore{draftStore: base, calls: map[string]int{}, unrelatedErr: errors.New("unrelated database query failed")}
			api := &libraryProbeImages{fakeImagesAPI: fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: 5, Name: "NAS", State: "unavailable", Error: "NAS offline"}}}}}
			recovery := &libraryProbeRecovery{fakeRecoveryAPI: fakeRecoveryAPI{status: agent.RecoveryStatus{LastError: "SMB reconnect failed"}}}
			usb := &fakeUEFICAUSB{archives: []ueficausb.Archive{{ProfileID: base.profiles[0].ID, Loader: "2011", Size: 613436355}}}
			s := &Server{Store: store, Images: api, StorageRecovery: recovery, UEFICAUSB: usb}
			body := libraryGet(t, s, path)
			for _, key := range []string{"answers", "authors", "image names", "image inspections"} {
				if store.calls[key] != 0 {
					t.Errorf("%s read unrelated %s %d times", path, key, store.calls[key])
				}
			}
			if store.calls["profiles"] != 1 {
				t.Errorf("profiles read %d times, want once", store.calls["profiles"])
			}
			wantReferences := 0
			if path == "/" {
				wantReferences = 1
			} // alerts and machine menu names
			for _, key := range []string{"driver configs", "installations", "boot profiles"} {
				if store.calls[key] != wantReferences {
					t.Errorf("%s read %s %d times, want %d", path, key, store.calls[key], wantReferences)
				}
			}
			wantCatalog, wantRecovery, wantAutoinstalls := 1, 1, 0
			if path == "/usb" {
				wantCatalog, wantRecovery = 0, 0
			}
			if path == "/" {
				wantAutoinstalls = 1
			} // machine menu names, outside library data
			if api.calls != wantCatalog || recovery.calls != wantRecovery || store.calls["autoinstalls"] != wantAutoinstalls {
				t.Errorf("unneeded source reads: catalog=%d recovery=%d autoinstalls=%d", api.calls, recovery.calls, store.calls["autoinstalls"])
			}
			switch path {
			case "/storages", "/system":
				if !strings.Contains(body, "NAS") || strings.Contains(body, "Не удалось прочитать сценарии Windows") {
					t.Errorf("storage content suppressed by unrelated failure: %.500s", body)
				}
			case "/usb":
				for _, want := range []string{base.profiles[0].Title, "585,0 МБ", "Скачать архив"} {
					if !strings.Contains(body, want) {
						t.Errorf("USB missing %q", want)
					}
				}
			case "/":
				for _, want := range []string{"NAS offline", "profile unavailable", "WinPE build failed", "SMB reconnect failed"} {
					if !strings.Contains(body, want) {
						t.Errorf("home alert missing %q", want)
					}
				}
			}
		})
	}
}
