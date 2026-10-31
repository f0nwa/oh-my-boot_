package ombmigration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Journal struct {
	Version                 int    `json:"schema_version"`
	ManifestDigest          string `json:"manifest_sha256"`
	Phase                   string `json:"phase"`
	Stage, Archive          string
	Before, After           Snapshot
	BeforeTrees, AfterTrees map[string][]FileRecord
	Preserved               []FileRecord
	Files                   []fileChange
	Published               map[string]bool
	Report                  Report
}

func (j Journal) completedReport(mode string) Report {
	r := j.Report
	r.Mode, r.Ready, r.Complete = mode, true, true
	r.Objects = append([]Status(nil), r.Objects...)
	for i := range r.Objects {
		r.Objects[i].State = "complete"
		if r.Objects[i].Kind == "menu" {
			r.Objects[i].SHA256 = text(j.After.find("pxe_control", "menu_versions", r.Objects[i].ID), "sha256")
		}
	}
	return r
}

func (e *Engine) readJournal() (Journal, error) {
	var j Journal
	b, err := privateRead(e.journalPath())
	if err != nil {
		return j, err
	}
	if decodeJSON(b, &j) != nil || j.Version != 1 || j.ManifestDigest != e.Manifest.Digest {
		return j, fmt.Errorf("journal does not match manifest")
	}
	stage, err := e.stagePath()
	if err != nil || j.Stage != stage {
		return j, fmt.Errorf("journal stage path differs")
	}
	switch j.Phase {
	case "building", "staged", "publishing", "complete":
	default:
		return j, fmt.Errorf("unknown partial migration phase; restore backup")
	}
	return j, nil
}
func (e *Engine) saveJournal(j Journal) error { return atomicJSON(e.journalPath(), j) }
func treeAt(full, logical string) ([]FileRecord, error) {
	records, err := treeRecords(filepath.Dir(full), "/"+filepath.Base(full))
	if err != nil {
		return nil, err
	}
	prefix := "/" + filepath.Base(full)
	for i := range records {
		records[i].Path = logical + strings.TrimPrefix(records[i].Path, prefix)
	}
	return records, nil
}
func matchesTree(full, logical string, want []FileRecord) bool {
	got, err := treeAt(full, logical)
	return err == nil && evidenceHash(got) == evidenceHash(want)
}
func (e *Engine) Apply(ctx context.Context) (Report, error) {
	if _, err := os.Lstat(e.journalPath()); err == nil {
		j, err := e.readJournal()
		if err != nil {
			return Report{Mode: "apply"}, err
		}
		if j.Phase == "complete" {
			if err = e.verifyJournal(ctx, j); err != nil {
				return Report{Mode: "apply"}, err
			}
			state, err := e.state()
			if err != nil {
				return Report{Mode: "apply"}, err
			}
			if state["data_complete"] != true {
				if err = MergeState(e.ManifestPath+".state.json", map[string]any{"data_complete": true, "data_journal": e.journalPath(), "data_manifest_sha256": e.Manifest.Digest}); err != nil {
					return Report{Mode: "apply"}, err
				}
			}
			return j.completedReport("apply"), nil
		}
	}
	backup, err := e.preconditions(ctx)
	if err != nil {
		return Report{Mode: "apply"}, err
	}
	lockPath := e.ManifestPath + ".data.lock"
	if _, err = safePath("/", lockPath); err != nil {
		return Report{}, err
	}
	lock, err := openPrivateLock(lockPath)
	if err != nil {
		return Report{}, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return Report{}, fmt.Errorf("data migration already running")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var j Journal
	if _, err = os.Lstat(e.journalPath()); os.IsNotExist(err) {
		in, err := e.inspect(ctx, true)
		if err != nil {
			return Report{Mode: "apply"}, err
		}
		stage, err := e.stagePath()
		if err != nil {
			return Report{}, err
		}
		if _, err = os.Lstat(stage); !os.IsNotExist(err) {
			return Report{}, fmt.Errorf("unowned staging directory already exists")
		}
		if err = os.Mkdir(e.ManifestPath+".data", 0700); err != nil {
			return Report{}, err
		}
		j = Journal{Version: 1, ManifestDigest: e.Manifest.Digest, Phase: "building", Stage: stage, Archive: filepath.Join(backup, "omb-data-"+e.Manifest.Digest[:16]), Before: in.Before, BeforeTrees: in.Trees, Preserved: in.Preserved, Files: in.Files, Published: map[string]bool{}, Report: in.Report}
		if err = e.saveJournal(j); err != nil {
			return Report{}, err
		}
	} else if err != nil {
		return Report{}, err
	} else {
		j, err = e.readJournal()
		if err != nil {
			return Report{}, err
		}
	}
	if j.Archive != filepath.Join(backup, "omb-data-"+e.Manifest.Digest[:16]) {
		return Report{}, fmt.Errorf("backup directory differs from journal")
	}
	if j.Phase == "building" {
		// A crash during staging cannot change canonical data. Discard only this
		// manifest's unreachable staging/archive after proving all source inputs.
		in, err := e.inspect(ctx, true)
		if err != nil {
			return Report{}, err
		}
		if snapshotHash(in.Before) != snapshotHash(j.Before) {
			return Report{}, fmt.Errorf("source changed during staging; restore backup")
		}
		for name, want := range j.BeforeTrees {
			if evidenceHash(in.Trees[name]) != evidenceHash(want) {
				return Report{}, fmt.Errorf("source assets changed during staging: %s", name)
			}
		}
		if err = checkRecords(e.Root, j.Preserved); err != nil {
			return Report{}, err
		}
		for _, f := range j.Files {
			if err = checkRecords(e.Root, []FileRecord{f.Before}); err != nil {
				return Report{}, err
			}
		}
		if err = os.RemoveAll(j.Stage); err != nil {
			return Report{}, err
		}
		if err = os.RemoveAll(j.Archive); err != nil {
			return Report{}, err
		}
		if err = os.MkdirAll(filepath.Join(j.Stage, "new"), 0700); err != nil {
			return Report{}, err
		}
		if err = os.Mkdir(j.Archive, 0700); err != nil {
			return Report{}, err
		}
		for _, name := range assetTrees {
			source, _ := e.path("/srv/omb/tftp/" + name)
			archived := filepath.Join(j.Archive, "assets", name)
			if err = copyTree(source, archived); err != nil {
				return Report{}, fmt.Errorf("asset archive failed: %s", name)
			}
			if !matchesTree(archived, "/srv/omb/tftp/"+name, j.BeforeTrees[name]) {
				return Report{}, fmt.Errorf("asset archive verification failed: %s", name)
			}
			if err = copyTree(source, filepath.Join(j.Stage, "new", name)); err != nil {
				return Report{}, err
			}
		}
		for _, f := range j.Files {
			source, _ := e.path(f.Path)
			b, err := os.ReadFile(source)
			if err != nil {
				return Report{}, err
			}
			target := filepath.Join(j.Archive, "files", strings.TrimPrefix(f.Path, "/"))
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return Report{}, err
			}
			if err = atomicWrite(target, b, 0600); err != nil {
				return Report{}, err
			}
		}
		if err = atomicJSON(filepath.Join(j.Archive, "source-databases.json"), j.Before); err != nil {
			return Report{}, err
		}
		builder := e.Rebuilder
		if builder == nil {
			builder = RuntimeRebuilder{}
		}
		built, err := builder.Rebuild(ctx, BuildInput{Root: e.Root, Stage: filepath.Join(j.Stage, "new"), Manifest: e.Manifest, Source: j.Before, XML: in.XML})
		if err != nil {
			return Report{}, err
		}
		if err = convertNoCloudStage(filepath.Join(j.Stage, "new", "ubuntu-autoinstall")); err != nil {
			return Report{}, err
		}
		j.After, err = transformedSnapshot(j.Before, built, in.XML)
		if err != nil {
			return Report{}, err
		}
		// The Boot snapshot is another atomic file publication of the same script.
		for _, row := range j.After.rows("pxe_control", "menu_versions") {
			if row["active"] == true {
				p := MapPath(text(e.Manifest.Boot, "path"))
				before, err := recordFile(e.Root, p)
				if err != nil {
					return Report{}, err
				}
				j.Files = append(j.Files, fileChange{Path: p, Before: before, After: []byte(text(row, "script"))})
			}
		}
		j.AfterTrees = map[string][]FileRecord{}
		for _, name := range assetTrees {
			full := filepath.Join(j.Stage, "new", name)
			if err = inheritAssetOwners(full, "/srv/omb/tftp/"+name, j.BeforeTrees[name]); err != nil {
				return Report{}, err
			}
			j.AfterTrees[name], err = treeAt(full, "/srv/omb/tftp/"+name)
			if err != nil {
				return Report{}, err
			}
		}
		j.Phase = "staged"
		if err = e.saveJournal(j); err != nil {
			return Report{}, err
		}
	}
	if j.Phase == "staged" {
		for _, name := range assetTrees {
			target, _ := e.path("/srv/omb/tftp/" + name)
			if !matchesTree(target, "/srv/omb/tftp/"+name, j.BeforeTrees[name]) || !matchesTree(filepath.Join(j.Stage, "new", name), "/srv/omb/tftp/"+name, j.AfterTrees[name]) {
				return Report{}, fmt.Errorf("staged assets differ: %s", name)
			}
		}
		j.Phase = "publishing"
		if err = e.saveJournal(j); err != nil {
			return Report{}, err
		}
	}
	if j.Phase == "publishing" {
		for _, name := range assetTrees {
			if err = e.publishTree(&j, name); err != nil {
				return Report{}, err
			}
		}
		for _, f := range j.Files {
			if strings.HasSuffix(f.Path, "/menu.ipxe") {
				continue
			}
			if err = e.publishFile(f); err != nil {
				return Report{}, err
			}
		}
		current, err := e.DB.Snapshot(ctx, true)
		if err != nil {
			return Report{}, err
		}
		for db := range j.Before {
			cur := Snapshot{db: current[db]}
			if snapshotHash(cur) != snapshotHash(Snapshot{db: j.Before[db]}) && snapshotHash(cur) != snapshotHash(Snapshot{db: j.After[db]}) {
				return Report{}, fmt.Errorf("unknown partial database state: %s; restore backup", db)
			}
		}
		if err = e.DB.Commit(ctx, current, j.After); err != nil {
			return Report{}, err
		}
		for _, f := range j.Files {
			if strings.HasSuffix(f.Path, "/menu.ipxe") {
				if err = e.publishFile(f); err != nil {
					return Report{}, err
				}
			}
		}
		if err = e.verifyJournal(ctx, j); err != nil {
			return Report{}, err
		}
		j.Phase = "complete"
		j.Report = j.completedReport("apply")
		if err = e.saveJournal(j); err != nil {
			return Report{}, err
		}
		if err = MergeState(e.ManifestPath+".state.json", map[string]any{"data_complete": true, "data_journal": e.journalPath(), "data_manifest_sha256": e.Manifest.Digest}); err != nil {
			return Report{}, err
		}
	}
	return j.Report, nil
}
func (e *Engine) publishTree(j *Journal, name string) error {
	logical := "/srv/omb/tftp/" + name
	target, err := e.path(logical)
	if err != nil {
		return err
	}
	staged := filepath.Join(j.Stage, "new", name)
	original := filepath.Join(j.Stage, "original", name)
	before, after := j.BeforeTrees[name], j.AfterTrees[name]
	if j.Published[name] {
		if !matchesTree(target, logical, after) {
			return fmt.Errorf("published assets changed: %s", name)
		}
		return nil
	}
	// A completed atomic rename is recognizable after a crash, independently of
	// whether its journal update reached disk.
	if matchesTree(target, logical, after) && len(after) > 0 && matchesTree(original, logical, before) {
		j.Published[name] = true
		return e.saveJournal(*j)
	}
	if len(before) == 0 && len(after) == 0 {
		j.Published[name] = true
		return e.saveJournal(*j)
	}
	if !matchesTree(staged, logical, after) {
		return fmt.Errorf("staged assets changed: %s", name)
	}
	if matchesTree(target, logical, before) && len(before) > 0 {
		if _, err = os.Lstat(original); !os.IsNotExist(err) {
			return fmt.Errorf("unknown archive state: %s", name)
		}
		if err = os.MkdirAll(filepath.Dir(original), 0700); err != nil {
			return err
		}
		if err = os.Rename(target, original); err != nil {
			return err
		}
	} else if len(before) > 0 && !matchesTree(original, logical, before) {
		return fmt.Errorf("unknown partial asset state: %s; restore backup", name)
	} else if _, err = os.Lstat(target); !os.IsNotExist(err) {
		return fmt.Errorf("unexpected target assets: %s", name)
	}
	if len(after) > 0 {
		if err = os.Rename(staged, target); err != nil {
			return err
		}
	}
	j.Published[name] = true
	return e.saveJournal(*j)
}
func (e *Engine) publishFile(f fileChange) error {
	p, err := e.path(f.Path)
	if err != nil {
		return err
	}
	actual, err := recordFile(e.Root, f.Path)
	if err != nil {
		return err
	}
	if actual.Hash == hashBytes(f.After) {
		if actual.Mode != f.Before.Mode || actual.UID != f.Before.UID || actual.GID != f.Before.GID {
			return fmt.Errorf("replacement file permissions differ: %s", f.Path)
		}
		return nil
	}
	if evidenceHash([]FileRecord{actual}) != evidenceHash([]FileRecord{f.Before}) {
		return fmt.Errorf("unknown partial file state: %s; restore backup", f.Path)
	}
	return writePreserving(p, f.After)
}
func (e *Engine) verifyJournal(ctx context.Context, j Journal) error {
	current, err := e.DB.Snapshot(ctx, true)
	if err != nil {
		return err
	}
	if snapshotHash(current) != snapshotHash(j.After) {
		return fmt.Errorf("database content differs from completed migration")
	}
	for _, name := range assetTrees {
		target, _ := e.path("/srv/omb/tftp/" + name)
		if !matchesTree(target, "/srv/omb/tftp/"+name, j.AfterTrees[name]) {
			return fmt.Errorf("asset verification failed: %s", name)
		}
	}
	changes := map[string]fileChange{}
	for _, f := range j.Files {
		changes[f.Path] = f
		actual, err := recordFile(e.Root, f.Path)
		if err != nil || actual.Hash != hashBytes(f.After) || actual.Mode != f.Before.Mode || actual.UID != f.Before.UID || actual.GID != f.Before.GID {
			return fmt.Errorf("file verification failed: %s", f.Path)
		}
	}
	for _, f := range j.Preserved {
		if _, changed := changes[f.Path]; changed {
			continue
		}
		if err = checkRecords(e.Root, []FileRecord{f}); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) Verify(ctx context.Context) (Report, error) {
	j, err := e.readJournal()
	if err != nil {
		return Report{Mode: "verify"}, err
	}
	if j.Phase != "complete" {
		return Report{Mode: "verify"}, fmt.Errorf("migration incomplete: phase %s", j.Phase)
	}
	if err = e.verifyJournal(ctx, j); err != nil {
		return Report{Mode: "verify"}, err
	}
	return j.completedReport("verify"), nil
}
