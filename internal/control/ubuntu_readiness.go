package control

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// UbuntuReadiness checks the shared Ubuntu source without transferring its ISO.
type UbuntuReadiness interface {
	Check(context.Context) error
}

// UbuntuReadinessError identifies the resource preventing an installation boot.
type UbuntuReadinessError struct {
	Resource string
	Err      error
}

func (e *UbuntuReadinessError) Error() string {
	return fmt.Sprintf("Ресурс Ubuntu %s недоступен: %s", e.Resource, e.Err.Error())
}
func (e *UbuntuReadinessError) Unwrap() error { return e.Err }

// HTTPUbuntuReadiness checks the same public URLs that iPXE uses. Boot maps
// the ISO URL to its explicitly configured file; Control needs no storage access.
type HTTPUbuntuReadiness struct{ BaseURL string }

func (c HTTPUbuntuReadiness) Check(ctx context.Context) error {
	base, err := validatedBootURL(c.BaseURL)
	if err != nil {
		return &UbuntuReadinessError{Resource: "ISO", Err: err}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, resource := range []struct{ name, path string }{
		{"ISO", "/images/ubuntu-desktop.iso"},
		{"vmlinuz", "/assets/ubuntu/vmlinuz"},
		{"initrd", "/assets/ubuntu/initrd"},
		{"bootx64.efi", "/assets/ubuntu/bootx64.efi"},
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, base+resource.path, nil)
		if err != nil {
			return &UbuntuReadinessError{Resource: resource.name, Err: err}
		}
		res, err := client.Do(req)
		if err != nil {
			return &UbuntuReadinessError{Resource: resource.name, Err: err}
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return &UbuntuReadinessError{Resource: resource.name, Err: fmt.Errorf("HTTP %d", res.StatusCode)}
		}
		if res.ContentLength <= 0 {
			return &UbuntuReadinessError{Resource: resource.name, Err: fmt.Errorf("empty or unknown file size")}
		}
	}
	return nil
}

func (s *Server) checkUbuntuReadiness(ctx context.Context) error {
	checker := s.UbuntuReadiness
	if checker == nil {
		checker = HTTPUbuntuReadiness{BaseURL: s.BootBaseURL}
	}
	err := checker.Check(ctx)
	s.ubuntuCacheMu.Lock()
	cache := s.ubuntuCache
	s.ubuntuCacheMu.Unlock()
	if cache != nil {
		cache.Update(err)
	}
	return err
}

func (s *Server) checkUbuntuReadinessCached(ctx context.Context) error {
	s.ubuntuCacheMu.Lock()
	if s.ubuntuCache == nil {
		s.ubuntuCache = newUbuntuReadinessCache(30*time.Second, 150*time.Millisecond)
	}
	cache := s.ubuntuCache
	s.ubuntuCacheMu.Unlock()
	return cache.Check(ctx, s.checkUbuntuReadiness)
}
