package control

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ubuntuSourceDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CreateUbuntuSource creates an identity for an ISO whose hash was already
// obtained by the trusted source checker. Repeated requests reuse that identity.
func (s Store) CreateUbuntuSource(ctx context.Context, storageID int64, isoPath, digest, title string) (BootProfile, error) {
	if storageID <= 0 || isoPath == "" || strings.HasPrefix(isoPath, "/") || strings.ContainsAny(isoPath, "\\\x00") || path.Clean(isoPath) != isoPath || isoPath == ".." || strings.HasPrefix(isoPath, "../") || !strings.EqualFold(path.Ext(isoPath), ".iso") || !ubuntuSourceDigest.MatchString(digest) || strings.TrimSpace(title) == "" {
		return BootProfile{}, fmt.Errorf("invalid Ubuntu source identity")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return BootProfile{}, err
	}
	defer tx.Rollback(ctx)
	key := strconv.FormatInt(storageID, 10) + ":" + isoPath + ":" + digest
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
		return BootProfile{}, err
	}
	var profile BootProfile
	err = tx.QueryRow(ctx, `SELECT b.id,b.kind,b.title,u.id,u.storage_id,u.path,u.sha256,u.state FROM ubuntu_sources u JOIN boot_profiles b ON b.id=u.boot_profile_id WHERE u.storage_id=$1 AND u.path=$2 AND u.sha256=$3`, storageID, isoPath, digest).Scan(&profile.ID, &profile.Kind, &profile.Title, &profile.UbuntuSourceID, &profile.UbuntuStorageID, &profile.UbuntuPath, &profile.UbuntuSHA256, &profile.UbuntuState)
	if err == pgx.ErrNoRows {
		profile = BootProfile{Kind: BootProfileUbuntuISO, Title: strings.TrimSpace(title), UbuntuStorageID: storageID, UbuntuPath: isoPath, UbuntuSHA256: digest, UbuntuState: "unverified"}
		if err = tx.QueryRow(ctx, `INSERT INTO boot_profiles(kind,title) VALUES($1,$2) RETURNING id`, profile.Kind, profile.Title).Scan(&profile.ID); err != nil {
			return BootProfile{}, err
		}
		if err = tx.QueryRow(ctx, `INSERT INTO ubuntu_sources(boot_profile_id,storage_id,path,sha256,state) VALUES($1,$2,$3,$4,$5) RETURNING id`, profile.ID, storageID, isoPath, digest, profile.UbuntuState).Scan(&profile.UbuntuSourceID); err != nil {
			return BootProfile{}, err
		}
	} else if err != nil {
		return BootProfile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BootProfile{}, err
	}
	return profile, nil
}

func (s Store) SetUbuntuSourceState(ctx context.Context, id int64, state, message string) error {
	if id <= 0 || len(message) > 2048 {
		return fmt.Errorf("invalid Ubuntu source state")
	}
	switch state {
	case "unverified", "checking", "verified", "preparing", "ready", "error":
	default:
		return fmt.Errorf("invalid Ubuntu source state")
	}
	result, err := s.Pool.Exec(ctx, `UPDATE ubuntu_sources SET state=$2,error=$3 WHERE id=$1`, id, state, message)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("Ubuntu source not found")
	}
	return nil
}
