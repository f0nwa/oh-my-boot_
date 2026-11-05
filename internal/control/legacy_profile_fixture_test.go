package control

import "context"

// EnsureBootProfiles builds the pre-migration fixture used by legacy tests.
func (s Store) EnsureBootProfiles(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO boot_profiles(kind,title) VALUES('ubuntu_builtin','Ubuntu Desktop') ON CONFLICT (kind) WHERE kind='ubuntu_builtin' DO NOTHING`)
	return err
}
