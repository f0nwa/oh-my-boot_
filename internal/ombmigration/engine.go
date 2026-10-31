package ombmigration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"omb/internal/agent"
)

type Database interface {
	Snapshot(context.Context, bool) (Snapshot, error)
	Commit(context.Context, Snapshot, Snapshot) error
}
type Engine struct {
	Root, ManifestPath string
	Manifest           Manifest
	DB                 Database
	Rebuilder          Rebuilder
	ServicesStopped    func(context.Context) error
}
type Status struct {
	Kind   string `json:"kind"`
	ID     int64  `json:"id,omitempty"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	State  string `json:"state"`
}
type Report struct {
	Mode     string   `json:"mode"`
	Ready    bool     `json:"ready"`
	Complete bool     `json:"complete"`
	Objects  []Status `json:"objects"`
}
type fileChange struct {
	Path   string
	Before FileRecord
	After  []byte
}
type inspection struct {
	Before    Snapshot
	Trees     map[string][]FileRecord
	Preserved []FileRecord
	Files     []fileChange
	XML       map[int64][]byte
	Report    Report
}

var assetTrees = []string{"windows-profiles", "windows-answers", "windows-driver-wims", "windows-driver-configs", "ubuntu-autoinstall"}

func sameCatalogTime(actual, stored time.Time) bool {
	return actual.Truncate(time.Microsecond).Equal(stored.Truncate(time.Microsecond))
}

func (e *Engine) path(p string) (string, error) { return safePath(e.Root, p) }
func (e *Engine) journalPath() string           { return e.ManifestPath + ".data/journal.json" }
func (e *Engine) stagePath() (string, error) {
	if len(e.Manifest.Digest) != 64 {
		return "", fmt.Errorf("manifest digest unavailable")
	}
	return e.path("/srv/omb/.omb-migrate-" + e.Manifest.Digest[:16])
}
func (e *Engine) Plan(ctx context.Context, renamed bool) (Report, error) {
	if _, err := os.Lstat(e.journalPath()); err == nil {
		j, err := e.readJournal()
		if err != nil {
			return Report{}, err
		}
		if j.Phase == "complete" {
			r, err := e.Verify(ctx)
			r.Mode = "plan"
			return r, err
		}
		return Report{}, fmt.Errorf("migration journal phase %s: use apply to resume or restore backup", j.Phase)
	}
	in, err := e.inspect(ctx, renamed)
	if err != nil {
		return Report{Mode: "plan"}, err
	}
	in.Report.Mode = "plan"
	in.Report.Ready = true
	return in.Report, nil
}
func (e *Engine) inspect(ctx context.Context, renamed bool) (inspection, error) {
	in := inspection{Trees: map[string][]FileRecord{}, XML: map[int64][]byte{}, Report: Report{Objects: []Status{}}}
	if e.DB == nil {
		return in, fmt.Errorf("database adapter unavailable")
	}
	snapshot, err := e.DB.Snapshot(ctx, renamed)
	if err != nil {
		return in, err
	}
	// A read-only preview can observe new journal entries while old services
	// run. Apply uses the strict snapshot frozen after system quiescence.
	if err = snapshot.validate(e.Manifest, !renamed); err != nil {
		return in, err
	}
	in.Before = snapshot
	logical := func(old string) string {
		if renamed {
			return MapPath(old)
		}
		return old
	}
	root := logical("/srv/pxe/tftp")
	for _, name := range assetTrees {
		records, err := treeRecords(e.Root, root+"/"+name)
		if err != nil {
			return in, fmt.Errorf("cannot inventory asset directory %s", name)
		}
		in.Trees[name] = records
	}
	bootPath := logical(text(e.Manifest.Boot, "path"))
	boot, err := recordFile(e.Root, bootPath)
	if err != nil || boot.Hash != text(e.Manifest.Boot, "sha256") {
		return in, fmt.Errorf("Boot snapshot differs from manifest")
	}
	in.Preserved = append(in.Preserved, boot)
	active := 0
	for _, row := range snapshot.rows("pxe_control", "menu_versions") {
		if row["active"] == true {
			active++
			if text(row, "sha256") != boot.Hash {
				return in, fmt.Errorf("active menu and Boot snapshot differ")
			}
		}
	}
	if active != 1 {
		return in, fmt.Errorf("expected exactly one active menu")
	}
	// Preserve opaque authentication/encryption material, TLS and the integration
	// key. Nothing in these files is parsed or rewritten.
	for _, f := range e.Manifest.Files {
		p := text(f, "path")
		if !strings.HasPrefix(p, "/var/lib/pxe-platform-control/") || text(f, "sha256") == "" {
			continue
		}
		r, err := recordFile(e.Root, logical(p))
		if err != nil || r.Hash != text(f, "sha256") {
			return in, fmt.Errorf("protected file differs: %s", logical(p))
		}
		in.Preserved = append(in.Preserved, r)
	}
	keyPath := logical("/var/lib/pxe-platform-control/integrations.key")
	key, err := recordFile(e.Root, keyPath)
	if err != nil {
		return in, fmt.Errorf("integration key unavailable")
	}
	in.Preserved = append(in.Preserved, key)
	profiles := map[int64]bool{}
	requests := map[int64]agent.WindowsISORequest{}
	for _, req := range e.Manifest.ProfileConfigs {
		if req.ID <= 0 || requests[req.ID].ID != 0 {
			return in, fmt.Errorf("invalid profile config IDs")
		}
		requests[req.ID] = req
	}
	for _, profile := range e.Manifest.Profiles {
		id := number(profile, "id")
		req, ok := requests[id]
		if !ok || req.StorageRoot != text(profile, "storage_root") || req.RelativePath != text(profile, "path") || req.Size != number(profile, "size_bytes") {
			return in, fmt.Errorf("profile config baseline differs: %d", id)
		}
		p := logical("/etc/pxe-platform/winiso/" + strconv.FormatInt(id, 10) + ".json")
		full, err := e.path(p)
		if err != nil {
			return in, err
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return in, fmt.Errorf("profile config unavailable: %d", id)
		}
		var actual agent.WindowsISORequest
		if decodeProfileConfig(b, &actual) != nil || actual.ID != req.ID || actual.RelativePath != req.RelativePath || actual.Size != req.Size || !actual.Modified.Equal(req.Modified) || actual.StorageRoot != req.StorageRoot {
			return in, fmt.Errorf("profile config differs: %d", id)
		}
		converted := actual
		converted.StorageRoot = MapPath(actual.StorageRoot)
		before, err := recordFile(e.Root, p)
		if err != nil {
			return in, err
		}
		if converted.StorageRoot != actual.StorageRoot {
			in.Files = append(in.Files, fileChange{Path: p, Before: before, After: mustJSON(converted)})
		}
		profiles[id] = true
		if text(profile, "state") == "ready" {
			sourcePath := logical(req.StorageRoot) + "/" + req.RelativePath
			fullSource, pathErr := e.path(sourcePath)
			if pathErr != nil {
				return in, pathErr
			}
			// Source ISO bytes are immutable external input. Validate the recorded
			// identity without hashing tens of gigabytes over CIFS on every pass.
			src, err := os.Lstat(fullSource)
			if err != nil || !src.Mode().IsRegular() || src.Size() != req.Size || !sameCatalogTime(src.ModTime(), req.Modified) {
				return in, fmt.Errorf("ISO source differs: profile %d", id)
			}
			for _, file := range []string{"boot.wim", "winpeshl.ini", "pxe-setup.cmd"} {
				p := root + "/windows-profiles/" + strconv.FormatInt(id, 10) + "/" + file
				if _, err = recordFile(e.Root, p); err != nil {
					return in, fmt.Errorf("base profile asset unavailable: %d", id)
				}
			}
		}
	}
	if len(requests) != len(e.Manifest.Profiles) {
		return in, fmt.Errorf("extra profile config observations")
	}
	repl := Replacements{Answers: map[int64]AnswerChange{}, Drivers: map[int64]DriverChange{}, Profiles: profiles}
	for _, answer := range e.Manifest.Answers {
		id := number(answer, "id")
		profileID := number(answer, "profile_id")
		repl.Answers[id] = AnswerChange{ProfileID: profileID, OldHash: text(answer, "sha256"), NewHash: text(answer, "sha256")}
		if text(answer, "state") != "ready" {
			continue
		}
		if !profiles[profileID] {
			return in, fmt.Errorf("answer parent unavailable: %d", id)
		}
		p := root + "/windows-answers/" + strconv.FormatInt(id, 10) + "/pxe-answer.xml"
		full, err := e.path(p)
		if err != nil {
			return in, err
		}
		b, err := os.ReadFile(full)
		if err != nil || hashBytes(b) != text(answer, "sha256") || int64(len(b)) != number(answer, "size_bytes") {
			return in, fmt.Errorf("answer canonical XML differs: %d", id)
		}
		converted, err := ConvertXML(b)
		if err != nil {
			return in, fmt.Errorf("answer %d: %w", id, err)
		}
		in.XML[id] = converted
		repl.Answers[id] = AnswerChange{ProfileID: profileID, OldHash: text(answer, "sha256"), NewHash: hashBytes(converted)}
		in.Report.Objects = append(in.Report.Objects, Status{Kind: "answer", ID: id, SHA256: hashBytes(converted), State: "planned"})
	}
	for _, driver := range e.Manifest.Drivers {
		id := number(driver, "id")
		repl.Drivers[id] = DriverChange{ProfileID: number(driver, "profile_id"), AnswerID: number(driver, "answer_id"), OldKey: text(driver, "wim_key"), NewKey: text(driver, "wim_key"), OldWIM: text(driver, "wim_sha256"), NewWIM: text(driver, "wim_sha256"), OldScript: text(driver, "script_sha256"), NewScript: text(driver, "script_sha256")}
		if text(driver, "state") != "ready" {
			continue
		}
		row := snapshot.find("pxe_control", "winpe_driver_configs", id)
		var selected []agent.SelectedINF
		if decodeJSON(mustJSON(row["selected_infs"]), &selected) != nil || len(selected) == 0 {
			return in, fmt.Errorf("driver selection unavailable: %d", id)
		}
		wim, err := recordFile(e.Root, root+"/windows-driver-wims/"+text(driver, "wim_key")+"/boot.wim")
		if err != nil || wim.Hash != text(driver, "wim_sha256") {
			return in, fmt.Errorf("driver WIM differs: %d", id)
		}
		script, err := recordFile(e.Root, root+"/windows-driver-configs/"+strconv.FormatInt(id, 10)+"/pxe-setup.cmd")
		if err != nil || script.Hash != text(driver, "script_sha256") {
			return in, fmt.Errorf("driver script differs: %d", id)
		}
		for _, inf := range selected {
			if !validHash(inf.PackageSHA256) || inf.INFPath == "" {
				return in, fmt.Errorf("invalid driver selection: %d", id)
			}
		}
	}
	for _, menu := range e.Manifest.Menus {
		id := number(menu, "id")
		row := snapshot.find("pxe_control", "menu_versions", id)
		if _, err := ConvertMenu([]byte(text(row, "script")), repl); err != nil {
			return in, fmt.Errorf("menu %d: %w", id, err)
		}
		in.Report.Objects = append(in.Report.Objects, Status{Kind: "menu", ID: id, SHA256: text(row, "sha256"), State: "planned"})
	}
	for _, oldPath := range []string{"/var/lib/pxe-platform-agent/network.json", "/etc/pxe-platform/network-full.json", "/etc/pxe-platform/network-proxy.json"} {
		p := logical(oldPath)
		full, err := e.path(p)
		if err != nil {
			return in, err
		}
		b, err := os.ReadFile(full)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return in, fmt.Errorf("network state unavailable")
		}
		var obj Object
		if decodeJSON(b, &obj) != nil {
			return in, fmt.Errorf("invalid network state: %s", p)
		}
		if old, ok := obj["tftp_root"].(string); ok && MapPath(old) != old {
			before, err := recordFile(e.Root, p)
			if err != nil {
				return in, err
			}
			obj["tftp_root"] = MapPath(old)
			in.Files = append(in.Files, fileChange{Path: p, Before: before, After: mustJSON(obj)})
		}
	}
	if err = validateNoCloud(e.Root, root, snapshot); err != nil {
		return in, err
	}
	return in, nil
}

func validateNoCloud(root, assets string, s Snapshot) error {
	tree, err := treeRecords(root, assets+"/ubuntu-autoinstall")
	if err != nil {
		return err
	}
	pairs := map[string]map[string]bool{}
	for _, f := range tree {
		if f.Directory {
			continue
		}
		rel := strings.TrimPrefix(f.Path, assets+"/ubuntu-autoinstall/")
		bits := strings.Split(rel, "/")
		if len(bits) != 3 || !validHash(bits[1]) || bits[2] != "user-data" && bits[2] != "meta-data" {
			return fmt.Errorf("unsupported NoCloud object: %s", f.Path)
		}
		id, e := strconv.ParseInt(bits[0], 10, 64)
		if e != nil || id <= 0 || strconv.FormatInt(id, 10) != bits[0] {
			return fmt.Errorf("invalid NoCloud installation ID")
		}
		pair := bits[0] + "/" + bits[1]
		if pairs[pair] == nil {
			pairs[pair] = map[string]bool{}
		}
		pairs[pair][bits[2]] = true
		if bits[2] == "user-data" && f.Hash != bits[1] {
			return fmt.Errorf("NoCloud user-data hash differs: %d", id)
		}
		if bits[2] == "meta-data" {
			p, _ := safePath(root, f.Path)
			b, e := os.ReadFile(p)
			want := "instance-id: pxe-installation-" + bits[0] + "-" + bits[1] + "\n"
			if e != nil || string(b) != want {
				return fmt.Errorf("NoCloud metadata differs: %d", id)
			}
		}
	}
	for _, files := range pairs {
		if !files["user-data"] || !files["meta-data"] {
			return fmt.Errorf("incomplete NoCloud pair")
		}
	}
	return nil
}
func (e *Engine) state() (map[string]any, error) {
	b, err := privateRead(e.ManifestPath + ".state.json")
	if err != nil {
		return nil, fmt.Errorf("private backup/system state unavailable")
	}
	var state map[string]any
	if decodeJSON(b, &state) != nil {
		return nil, fmt.Errorf("invalid backup/system state")
	}
	return state, nil
}
func (e *Engine) preconditions(ctx context.Context) (string, error) {
	state, err := e.state()
	if err != nil {
		return "", err
	}
	if state["backup_complete"] != true || state["backup_verified"] != true || state["system_complete"] != true {
		return "", fmt.Errorf("verified backup and completed system migration required")
	}
	if state["manifest_sha256"] != e.Manifest.Digest || state["rollback_complete"] == true {
		return "", fmt.Errorf("system backup state does not match this migration manifest")
	}
	backup, _ := state["backup_dir"].(string)
	if !filepath.IsAbs(backup) || filepath.Clean(backup) != backup {
		return "", fmt.Errorf("invalid backup directory")
	}
	p, err := safePath("/", backup)
	if err != nil {
		return "", err
	}
	i, err := os.Lstat(p)
	if err != nil || !i.IsDir() || i.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("unsafe backup directory")
	}
	for _, own := range []string{"/srv/omb", "/etc/omb", "/var/lib/omb-agent", "/var/lib/omb-boot", "/var/lib/omb-control", "/var/lib/omb-images"} {
		full, _ := e.path(own)
		if backup == full || strings.HasPrefix(backup, full+"/") {
			return "", fmt.Errorf("backup is inside migrated directory")
		}
	}
	if e.ServicesStopped == nil {
		return "", fmt.Errorf("service stop verifier unavailable")
	}
	if err = e.ServicesStopped(ctx); err != nil {
		return "", err
	}
	for _, old := range []string{"/srv/pxe", "/etc/pxe-platform", "/var/lib/pxe-platform-control", "/var/lib/pxe-platform-boot"} {
		p, _ := e.path(old)
		if _, err = os.Lstat(p); !os.IsNotExist(err) {
			return "", fmt.Errorf("legacy installation path remains: %s", old)
		}
	}
	return backup, nil
}
