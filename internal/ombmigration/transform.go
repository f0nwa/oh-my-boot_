package ombmigration

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func transformedSnapshot(before Snapshot, built BuildResult, xml map[int64][]byte) (Snapshot, error) {
	after := cloneSnapshot(before)
	profiles := map[int64]bool{}
	for _, r := range after.rows("pxe_images", "storages") {
		r["root"] = MapPath(text(r, "root"))
	}
	for _, r := range after.rows("pxe_control", "windows_profiles") {
		r["storage_root"] = MapPath(text(r, "storage_root"))
		profiles[number(r, "id")] = true
	}
	for _, r := range after.rows("pxe_control", "windows_answer_variants") {
		id := number(r, "id")
		a, ok := built.Answers[id]
		if !ok || a.ProfileID != number(r, "profile_id") || a.OldHash != text(r, "sha256") {
			return nil, fmt.Errorf("missing rebuilt answer %d", id)
		}
		if text(r, "state") == "ready" {
			if hashBytes(xml[id]) != a.NewHash {
				return nil, fmt.Errorf("rebuilt answer hash differs: %d", id)
			}
			r["sha256"] = a.NewHash
			r["size_bytes"] = int64(len(xml[id]))
		}
	}
	for _, r := range after.rows("pxe_control", "winpe_driver_configs") {
		id := number(r, "id")
		d, ok := built.Drivers[id]
		if !ok || d.ProfileID != number(r, "profile_id") || d.AnswerID != number(r, "answer_id") || d.OldKey != text(r, "wim_key") || d.OldWIM != text(r, "wim_sha256") || d.OldScript != text(r, "script_sha256") {
			return nil, fmt.Errorf("missing rebuilt driver %d", id)
		}
		if text(r, "state") == "ready" {
			if !validHash(d.NewKey) || !validHash(d.NewWIM) || !validHash(d.NewScript) {
				return nil, fmt.Errorf("invalid rebuilt driver hashes: %d", id)
			}
			r["wim_key"] = d.NewKey
			r["wim_sha256"] = d.NewWIM
			r["script_sha256"] = d.NewScript
		}
	}
	repl := Replacements{Answers: built.Answers, Drivers: built.Drivers, Profiles: profiles}
	for _, r := range after.rows("pxe_control", "menu_versions") {
		script, err := ConvertMenu([]byte(text(r, "script")), repl)
		if err != nil {
			return nil, fmt.Errorf("menu %d: %w", number(r, "id"), err)
		}
		r["script"] = string(script)
		r["sha256"] = hashBytes(script)
	}
	return after, nil
}
func convertNoCloudStage(root string) error {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "meta-data" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		bits := strings.Split(filepath.ToSlash(rel), "/")
		if len(bits) != 3 {
			return fmt.Errorf("invalid staged NoCloud metadata")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		want := "instance-id: pxe-installation-" + bits[0] + "-" + bits[1] + "\n"
		if string(b) != want {
			return fmt.Errorf("staged NoCloud metadata differs")
		}
		return writePreserving(p, []byte("instance-id: omb-installation-"+bits[0]+"-"+bits[1]+"\n"))
	})
}
func inheritAssetOwners(root, logical string, before []FileRecord) error {
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	}
	originals := map[string]FileRecord{}
	for _, r := range before {
		originals[r.Path] = r
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in staged assets")
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		target := logical
		if rel != "." {
			target = logical + "/" + filepath.ToSlash(rel)
		}
		old := target
		switch filepath.Base(old) {
		case "omb-setup.cmd":
			old = filepath.Join(filepath.Dir(old), "pxe-setup.cmd")
		case "omb-answer.xml":
			old = filepath.Join(filepath.Dir(old), "pxe-answer.xml")
		}
		original, ok := originals[old]
		if !ok {
			ancestor := filepath.Dir(old)
			for strings.HasPrefix(ancestor, logical) {
				if candidate, found := originals[ancestor]; found {
					original = candidate
					ok = true
					break
				}
				if ancestor == logical {
					break
				}
				ancestor = filepath.Dir(ancestor)
			}
		}
		if ok {
			if err = os.Chown(p, int(original.UID), int(original.GID)); err != nil {
				return err
			}
			if original.Directory == d.IsDir() {
				if err = os.Chmod(p, os.FileMode(original.Mode)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
