package control

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
)

type imageAddedStore struct {
	*downloadsStore
	uploads map[string]time.Time
}

func (s *imageAddedStore) ResourceAuthors(context.Context) (ResourceAuthors, error) {
	return ResourceAuthors{ImageUploadedAt: s.uploads}, nil
}

func TestImageAddedColumnUsesUploadOrDiscoveryDate(t *testing.T) {
	s, store, _ := downloadsServer(t)
	discovered := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	uploaded := time.Date(2026, 10, 30, 12, 15, 0, 0, time.UTC)
	api := s.Images.(*fakeImagesAPI)
	for i := range api.catalog.Images {
		api.catalog.Images[i].FirstSeenAt = &discovered
	}
	first := api.catalog.Images[0]
	s.Store = &imageAddedStore{downloadsStore: store, uploads: map[string]time.Time{imageAuthorKey(first.StorageID, first.Path): uploaded}}
	page := libraryGet(t, s, "/images")
	if !strings.Contains(page, ">Добавлен</th>") || !strings.Contains(page, `datetime="`+uploaded.Format(time.RFC3339)+`"`) || !strings.Contains(page, `datetime="`+discovered.Format(time.RFC3339)+`"`) || !strings.Contains(page, "Загружен") || !strings.Contains(page, "Обнаружен") {
		t.Fatal("missing addition dates or their source")
	}
}

func TestFirstSeenPersistsAcrossScansAndUpload(t *testing.T) {
	control := installationTestStore(t)
	ctx := context.Background()
	schema, err := os.ReadFile("../../deploy/sql/images.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := control.Pool.QueryRow(ctx, `INSERT INTO storages(name,kind,root,state) VALUES('NAS','smb','/mnt/omb-smb/test','ready') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	store := images.Store{Pool: control.Pool}
	found := []images.Image{{Path: "scan.iso", Size: 1234, Modified: time.Now()}}
	if err := store.SaveScan(ctx, id, found, 777); err != nil {
		t.Fatal(err)
	}
	catalog, err := store.Catalog(ctx)
	if err != nil || len(catalog.Images) != 1 || catalog.Images[0].FirstSeenAt == nil {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	if len(catalog.Storages) != 1 || catalog.Storages[0].ISOBytes != 1234 || catalog.Storages[0].OtherBytes != 777 {
		t.Fatalf("storage byte counts mismatch: %+v", catalog.Storages)
	}
	original := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	if _, err := control.Pool.Exec(ctx, `UPDATE images SET first_seen_at=$1`, original); err != nil {
		t.Fatal(err)
	}
	found[0].Size++
	if err := store.SaveScan(ctx, id, found, 555); err != nil {
		t.Fatal(err)
	}
	catalog, err = store.Catalog(ctx)
	if err != nil || catalog.Images[0].FirstSeenAt == nil || !catalog.Images[0].FirstSeenAt.Equal(original) {
		t.Fatalf("rescan reset discovery: %+v %v", catalog, err)
	}
	if err := store.RecordUpload(ctx, images.Image{StorageID: id, Path: "upload.iso", Size: 1234, Modified: time.Now()}); err != nil {
		t.Fatal(err)
	}
	catalog, err = store.Catalog(ctx)
	if err != nil || len(catalog.Images) != 2 || catalog.Images[1].FirstSeenAt == nil {
		t.Fatalf("upload date not recorded: %+v %v", catalog, err)
	}
}

func TestHistoricalUploadDateSurvivesDeletedActor(t *testing.T) {
	store := installationTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 10, 20, 8, 30, 0, 0, time.UTC)
	if _, err := store.Pool.Exec(ctx, `INSERT INTO audit_events(occurred_at,actor_id,action,result,detail) VALUES($1,NULL,'iso.upload','ok','7 folder/with space.iso digest')`, at); err != nil {
		t.Fatal(err)
	}
	authors, err := store.ResourceAuthors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !authors.ImageUploadedAt[imageAuthorKey(7, "folder/with space.iso")].Equal(at) {
		t.Fatalf("upload time lost: %+v", authors)
	}
}

func TestLegacyDiscoveryDateStaysUnknownAfterMigration(t *testing.T) {
	control := installationTestStore(t)
	ctx := context.Background()
	schema, err := os.ReadFile("../../deploy/sql/images.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := control.Pool.Exec(ctx, `ALTER TABLE images DROP COLUMN first_seen_at; INSERT INTO storages(name,kind,root) VALUES('NAS','smb','/mnt/omb-smb/key7'); INSERT INTO images(storage_id,path,size_bytes,modified_at) SELECT id,'legacy.iso',1234,now() FROM storages`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../deploy/sql/2026-11-05-image-first-seen.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := control.Pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	store := images.Store{Pool: control.Pool}
	catalog, err := store.Catalog(ctx)
	if err != nil || len(catalog.Images) != 1 || catalog.Images[0].FirstSeenAt != nil {
		t.Fatalf("migration invented a date: %+v %v", catalog, err)
	}
	if err := store.SaveScan(ctx, catalog.Storages[0].ID, catalog.Images, 0); err != nil {
		t.Fatal(err)
	}
	catalog, err = store.Catalog(ctx)
	if err != nil || catalog.Images[0].FirstSeenAt != nil {
		t.Fatal("rescan invented a historical date")
	}
}

func TestLegacyISOColumnDoesNotUseFileModificationTime(t *testing.T) {
	s, _, _ := downloadsServer(t)
	page := libraryGet(t, s, "/images")
	if strings.Contains(page, `<time datetime=`) {
		t.Fatal("historical modification times used as addition dates")
	}
}
