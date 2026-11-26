package control

import (
	"context"
	"time"

	"omb/internal/agent"
	"omb/internal/images"
)

// ImageInspection is valid only for the catalog snapshot that Agent inspected.
type ImageInspection struct {
	StorageID               int64
	Path, StorageRoot, Kind string
	Version, Arch, Summary  string
	WIMInfo                 agent.WindowsWIMInfo
	Size                    int64
	Modified                time.Time
}

type imageInspectionStore interface {
	ImageInspections(context.Context) (map[string]ImageInspection, error)
	SetImageInspection(context.Context, ImageInspection) error
}

func inspectionRecord(source WindowsProfileSource, kind string) ImageInspection {
	return ImageInspection{StorageID: source.StorageID, Path: source.Path, StorageRoot: source.StorageRoot, Size: source.Size, Modified: source.Modified, Kind: kind}
}
func inspectionRecordFromResult(source WindowsProfileSource, kind string, result agent.ISOInspection) ImageInspection {
	rec := inspectionRecord(source, kind)
	if kind != "error" && kind != "unsupported" {
		rec.Version = result.Version
		rec.Arch = result.Arch
		rec.Summary = result.Summary
		rec.WIMInfo = result.WIMInfo
	}
	return rec
}
func (record ImageInspection) Matches(source WindowsProfileSource) bool {
	return record.StorageID == source.StorageID && record.Path == source.Path && record.StorageRoot == source.StorageRoot && record.Size == source.Size && record.Modified.Truncate(time.Microsecond).Equal(source.Modified.Truncate(time.Microsecond))
}
func (s Store) ImageInspections(ctx context.Context) (map[string]ImageInspection, error) {
	rows, err := s.Pool.Query(ctx, "SELECT storage_id,path,storage_root,size_bytes,modified_at,kind,version,arch,summary,wim_info FROM image_inspections")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := map[string]ImageInspection{}
	for rows.Next() {
		var r ImageInspection
		if err := rows.Scan(&r.StorageID, &r.Path, &r.StorageRoot, &r.Size, &r.Modified, &r.Kind, &r.Version, &r.Arch, &r.Summary, &r.WIMInfo); err != nil {
			return nil, err
		}
		records[imageAuthorKey(r.StorageID, r.Path)] = r
	}
	return records, rows.Err()
}
func (s Store) SetImageInspection(ctx context.Context, r ImageInspection) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO image_inspections(storage_id,path,storage_root,size_bytes,modified_at,kind,version,arch,summary,wim_info) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(storage_id,path) DO UPDATE SET storage_root=EXCLUDED.storage_root,size_bytes=EXCLUDED.size_bytes,modified_at=EXCLUDED.modified_at,kind=EXCLUDED.kind,version=EXCLUDED.version,arch=EXCLUDED.arch,summary=EXCLUDED.summary,wim_info=EXCLUDED.wim_info`, r.StorageID, r.Path, r.StorageRoot, r.Size, r.Modified, r.Kind, r.Version, r.Arch, r.Summary, r.WIMInfo)
	return err
}
func (s Store) DeleteImageInspection(ctx context.Context, id int64, path string) error {
	if s.Pool == nil {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `DELETE FROM image_inspections WHERE storage_id=$1 AND path=$2`, id, path)
	return err
}
func (s *Server) imageInspections(ctx context.Context) (map[string]ImageInspection, error) {
	if store, ok := s.Store.(imageInspectionStore); ok {
		return store.ImageInspections(ctx)
	}
	return map[string]ImageInspection{}, nil
}
func currentImageInspection(catalog images.Catalog, records map[string]ImageInspection, id int64, path string) (ImageInspection, bool) {
	source, err := SelectWindowsISO(catalog, id, path, "ISO", "")
	if err != nil {
		return ImageInspection{}, false
	}
	record, ok := records[imageAuthorKey(id, path)]
	return record, ok && record.Matches(source)
}
func inspectionLabel(kind string) (label, problem, arch string) {
	switch kind {
	case "windows":
		return "Windows", "", "x64"
	case "strelec":
		return "WinPE Strelec", "", "x64"
	case "ubuntu":
		return "Ubuntu", "", "x64"
	case "unsupported":
		return "Не поддерживается", "Поддерживаемая структура ISO не найдена", "—"
	case "error":
		return "Не проверен", "Проверка ISO не завершена", "—"
	default:
		return "Не проверен", "", "—"
	}
}

// Filter a whole catalog once; library and picker rendering must not rescan it
// separately for every ISO.
func matchingImageInspections(catalog images.Catalog, records map[string]ImageInspection) map[string]ImageInspection {
	storages := make(map[int64]images.Storage, len(catalog.Storages))
	for _, s := range catalog.Storages {
		storages[s.ID] = s
	}
	current := make(map[string]ImageInspection)
	for _, image := range catalog.Images {
		storage, ok := storages[image.StorageID]
		if !ok || !image.Present || storage.State != "ready" || !(storage.Kind == "smb" || storage.Kind == "nfs" || storage.Kind == "local" && localISOStorage(storage.Root)) {
			continue
		}
		key := imageAuthorKey(image.StorageID, image.Path)
		record, ok := records[key]
		if ok && record.Matches(WindowsProfileSource{StorageID: image.StorageID, Path: image.Path, StorageRoot: storage.Root, Size: image.Size, Modified: image.Modified}) {
			current[key] = record
		}
	}
	return current
}
