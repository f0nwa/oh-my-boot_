package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ResourceAuthors resolves the original actor recorded when a library resource
// was created or uploaded. The first successful event owns a content-addressed
// resource, even when the same file is uploaded again later.
type ResourceAuthors struct {
	Installations   map[int64]string
	Answers         map[int64]string
	Images          map[string]string
	ImageUploadedAt map[string]time.Time
	Drivers         map[string]string
}

func (a ResourceAuthors) clone() ResourceAuthors {
	out := ResourceAuthors{
		Installations:   make(map[int64]string, len(a.Installations)),
		Answers:         make(map[int64]string, len(a.Answers)),
		Images:          make(map[string]string, len(a.Images)),
		ImageUploadedAt: make(map[string]time.Time, len(a.ImageUploadedAt)),
		Drivers:         make(map[string]string, len(a.Drivers)),
	}
	for k, v := range a.Installations {
		out.Installations[k] = v
	}
	for k, v := range a.Answers {
		out.Answers[k] = v
	}
	for k, v := range a.Images {
		out.Images[k] = v
	}
	for k, v := range a.ImageUploadedAt {
		out.ImageUploadedAt[k] = v
	}
	for k, v := range a.Drivers {
		out.Drivers[k] = v
	}
	return out
}

type resourceAuthorsCache struct {
	mu      sync.RWMutex
	data    ResourceAuthors
	expires time.Time
	valid   bool
}

var defaultResourceAuthorsCache resourceAuthorsCache

const resourceAuthorsTTL = 60 * time.Second

func (c *resourceAuthorsCache) get(now time.Time) (ResourceAuthors, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.valid && now.Before(c.expires) {
		return c.data.clone(), true
	}
	return ResourceAuthors{}, false
}

func (c *resourceAuthorsCache) set(authors ResourceAuthors, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = authors.clone()
	c.expires = now.Add(resourceAuthorsTTL)
	c.valid = true
}

func (c *resourceAuthorsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.valid = false
	c.data = ResourceAuthors{}
	c.expires = time.Time{}
}

// InvalidateResourceAuthorsCache clears the cached ResourceAuthors on the server.
func (s *Server) InvalidateResourceAuthorsCache() {
	if s != nil {
		s.authorsCache.invalidate()
	}
	defaultResourceAuthorsCache.invalidate()
}

// InvalidateResourceAuthorsCache invalidates the global default ResourceAuthors cache.
func InvalidateResourceAuthorsCache() {
	defaultResourceAuthorsCache.invalidate()
}

func (s *Server) resourceAuthors(ctx context.Context) ResourceAuthors {
	if s == nil {
		return ResourceAuthors{}
	}
	now := time.Now()
	if cached, ok := s.authorsCache.get(now); ok {
		return cached
	}

	store, ok := s.Store.(interface {
		ResourceAuthors(context.Context) (ResourceAuthors, error)
	})
	if !ok {
		return ResourceAuthors{}
	}
	authors, err := store.ResourceAuthors(ctx)
	if err != nil {
		return ResourceAuthors{}
	}
	s.authorsCache.set(authors, now)
	return authors.clone()
}

func imageAuthorKey(storageID int64, path string) string {
	return fmt.Sprintf("%d:%s", storageID, path)
}

func (s Store) ResourceAuthors(ctx context.Context) (ResourceAuthors, error) {
	rows, err := s.Pool.Query(ctx, `SELECT a.action,a.detail,COALESCE(u.username,''),a.occurred_at
		FROM audit_events a LEFT JOIN users u ON u.id=a.actor_id
		WHERE a.result='ok' AND a.action IN ('installation.create','windows-answer.create','iso.upload','driver.import')
		ORDER BY a.id`)
	if err != nil {
		return ResourceAuthors{}, err
	}
	defer rows.Close()
	authors := ResourceAuthors{
		Installations: map[int64]string{}, Answers: map[int64]string{},
		Images: map[string]string{}, Drivers: map[string]string{}, ImageUploadedAt: map[string]time.Time{},
	}
	for rows.Next() {
		var action, detail, actor string
		var at time.Time
		if err := rows.Scan(&action, &detail, &actor, &at); err != nil {
			return ResourceAuthors{}, err
		}
		if actor == "" && action != "iso.upload" {
			continue
		}
		switch action {
		case "installation.create":
			id, err := strconv.ParseInt(strings.TrimPrefix(detail, "install_"), 10, 64)
			if err == nil && id > 0 {
				authors.Installations[id] = actor
			}
		case "windows-answer.create":
			for _, field := range strings.Fields(detail) {
				if strings.HasPrefix(field, "id=") {
					id, err := strconv.ParseInt(strings.TrimPrefix(field, "id="), 10, 64)
					if err == nil && id > 0 {
						authors.Answers[id] = actor
					}
					break
				}
			}
		case "iso.upload":
			firstSpace := strings.IndexByte(detail, ' ')
			lastSpace := strings.LastIndexByte(detail, ' ')
			if firstSpace <= 0 || lastSpace <= firstSpace {
				continue
			}
			storageID, err := strconv.ParseInt(detail[:firstSpace], 10, 64)
			if err == nil && storageID > 0 {
				path := detail[firstSpace+1 : lastSpace]
				key := imageAuthorKey(storageID, path)
				if _, exists := authors.ImageUploadedAt[key]; !exists {
					authors.ImageUploadedAt[key] = at
				}
				if authors.Images[key] == "" {
					authors.Images[key] = actor
				}
			}
		case "driver.import":
			fields := strings.Fields(detail)
			if len(fields) == 0 || strings.Contains(fields[0], "=") || len(fields) > 1 && fields[1] == "existing" {
				continue
			}
			if authors.Drivers[fields[0]] == "" {
				authors.Drivers[fields[0]] = actor
			}
		}
	}
	return authors, rows.Err()
}
