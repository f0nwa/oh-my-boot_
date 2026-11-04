package control

import (
	"context"
	"fmt"
	"net/http"
	"omb/internal/autoinstall"
	"strconv"
	"time"
)

type UbuntuSourceAPI interface {
	UbuntuSource(context.Context) (autoinstall.SourceCatalog, error)
}

type UbuntuSourceForAPI interface {
	UbuntuSourceFor(context.Context, int64, string) (autoinstall.SourceCatalog, error)
}

func (s *Server) selectedUbuntuEditorSource(r *http.Request) (int64, string, error) {
	raw := r.FormValue("iso_source_id")
	if raw == "" || raw == "0" {
		return 0, "", nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, "", fmt.Errorf("source: invalid ISO identity")
	}
	profiles, err := s.Store.BootProfiles(r.Context())
	if err != nil {
		return 0, "", fmt.Errorf("source: ISO catalog unavailable")
	}
	for _, profile := range profiles {
		if profile.Kind == BootProfileUbuntuISO && profile.UbuntuSourceID == id && profile.UbuntuState == "ready" && ubuntuSourceDigest.MatchString(profile.UbuntuSHA256) {
			return id, profile.UbuntuSHA256, nil
		}
	}
	return 0, "", fmt.Errorf("source: selected ISO unavailable")
}

type UbuntuLegacySourceAPI interface {
	UbuntuLegacySource(context.Context) (int64, string, error)
}

func (s *Server) verifyLegacyUbuntuAlias(ctx context.Context, version MenuVersion, sources InstallationMenuSources) error {
	if version.ID == 0 || version.LegacyUbuntuSourceID == 0 {
		return nil
	}
	provider, ok := s.UbuntuSource.(UbuntuLegacySourceAPI)
	if !ok && s.Boot != nil {
		provider = s.Boot
	}
	if provider == nil {
		return fmt.Errorf("legacy Ubuntu source unavailable")
	}
	id, digest, err := provider.UbuntuLegacySource(ctx)
	if err != nil || id != version.LegacyUbuntuSourceID {
		return fmt.Errorf("legacy Ubuntu source differs from published menu")
	}
	for _, profile := range sources.BootProfiles {
		if profile.Kind == BootProfileUbuntuBuiltin && profile.UbuntuSourceID == id && profile.UbuntuSHA256 == digest && profile.UbuntuState == "ready" {
			return nil
		}
	}
	return fmt.Errorf("legacy Ubuntu source differs from published menu")
}

func (s *Server) ubuntuSourceCatalogFor(ctx context.Context, id int64, digest string) (autoinstall.SourceCatalog, error) {
	if id <= 0 || !ubuntuSourceDigest.MatchString(digest) {
		return autoinstall.SourceCatalog{}, fmt.Errorf("source: invalid ISO identity")
	}
	provider, ok := s.UbuntuSource.(UbuntuSourceForAPI)
	if !ok && s.Boot != nil {
		provider = s.Boot
	}
	if provider == nil {
		return autoinstall.SourceCatalog{}, fmt.Errorf("source: verified ISO metadata unavailable")
	}
	catalog, err := provider.UbuntuSourceFor(ctx, id, digest)
	if err != nil || catalog.ISOSHA256 != digest {
		return autoinstall.SourceCatalog{}, fmt.Errorf("source: verified ISO metadata unavailable")
	}
	return catalog, nil
}

func (s *Server) validateUbuntuSourceFor(ctx context.Context, v autoinstall.Values, id int64, digest string) error {
	if id == 0 {
		return s.validateUbuntuSource(ctx, v)
	}
	catalog, err := s.ubuntuSourceCatalogFor(ctx, id, digest)
	if err != nil {
		return err
	}
	return autoinstall.ValidateSource(v, catalog)
}

func (s *Server) validateUbuntuEditorSource(ctx context.Context, v autoinstall.Values, id int64, digest string) error {
	if id == 0 {
		profiles, err := s.Store.BootProfiles(ctx)
		if err != nil {
			return fmt.Errorf("source: ISO catalog unavailable")
		}
		for _, profile := range profiles {
			if profile.Kind == BootProfileUbuntuISO {
				if v.SourceID != "" {
					return fmt.Errorf("source: select an ISO before choosing an application set")
				}
				return nil
			}
		}
	}
	return s.validateUbuntuSourceFor(ctx, v, id, digest)
}

func (s *Server) ubuntuSourceCatalog(ctx context.Context) (autoinstall.SourceCatalog, error) {
	provider := s.UbuntuSource
	if provider == nil && s.Boot != nil {
		provider = s.Boot
	}
	if provider == nil {
		return autoinstall.SourceCatalog{}, fmt.Errorf("source: verified ISO metadata unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return provider.UbuntuSource(ctx)
}
func (s *Server) validateUbuntuSource(ctx context.Context, v autoinstall.Values) error {
	if v.SourceID == "" {
		return nil
	}
	catalog, err := s.ubuntuSourceCatalog(ctx)
	if err != nil {
		return fmt.Errorf("source: verified ISO metadata unavailable")
	}
	return autoinstall.ValidateSource(v, catalog)
}
