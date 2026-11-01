package control

import (
	"context"
	"fmt"
	"omb/internal/autoinstall"
	"time"
)

type UbuntuSourceAPI interface {
	UbuntuSource(context.Context) (autoinstall.SourceCatalog, error)
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
