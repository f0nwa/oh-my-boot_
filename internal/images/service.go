package images

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type ScanRepository interface {
	Storage(context.Context, int64) (Storage, error)
	SaveScan(context.Context, int64, []Image) error
	ScanError(context.Context, int64, string) error
}

func ScanStorage(ctx context.Context, repo ScanRepository, id int64) error {
	return scanStorageWith(ctx, repo, id, MountIdentity, Scan)
}

func scanStorageWith(ctx context.Context, repo ScanRepository, id int64, mountIdentity func(string) (string, bool), scan func(string) ([]Image, error)) error {
	fail := func(scanErr error) error {
		failureCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := repo.ScanError(failureCtx, id, scanErr.Error()); err != nil {
			return errors.Join(scanErr, fmt.Errorf("record scan failure: %w", err))
		}
		return scanErr
	}
	storage, err := repo.Storage(ctx, id)
	if err != nil {
		return fail(err)
	}
	var mountBefore string
	if storage.Kind == "mounted" {
		var ok bool
		mountBefore, ok = mountIdentity(storage.Root)
		if !ok {
			return fail(fmt.Errorf("storage %q is not mounted", storage.Name))
		}
	}
	found, err := scan(storage.Root)
	if err != nil {
		return fail(err)
	}
	if storage.Kind == "mounted" {
		mountAfter, ok := mountIdentity(storage.Root)
		if !ok || mountAfter != mountBefore {
			return fail(fmt.Errorf("storage %q mount changed during scan", storage.Name))
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := repo.SaveScan(ctx, id, found); err != nil {
		return fail(err)
	}
	return nil
}
