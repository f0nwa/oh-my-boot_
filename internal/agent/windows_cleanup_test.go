package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveWindowsProfileLeavesSourceISO(t *testing.T) {
	base := t.TempDir()
	configRoot, mountRoot, assetRoot := filepath.Join(base, "configs"), filepath.Join(base, "mounts"), filepath.Join(base, "assets")
	for _, root := range []string{configRoot, mountRoot, assetRoot} {
		if err := os.Mkdir(root, 0755); err != nil {
			t.Fatal(err)
		}
	}
	iso := filepath.Join(base, "source.iso")
	if err := os.WriteFile(iso, []byte("source"), 0444); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(mountRoot, "17"), filepath.Join(assetRoot, "17")} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configRoot, "17.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	mounter := &fakeWindowsISOMounter{mounted: true}
	systemctl := &fakeWindowsISOSystemctl{}
	m := &WindowsISOManager{ConfigRoot: configRoot, MountRoot: mountRoot, AssetRoot: assetRoot, Mounter: mounter, Systemctl: systemctl}
	for i := 0; i < 2; i++ {
		if err := m.RemoveProfile(context.Background(), 17); err != nil {
			t.Fatal(err)
		}
	}
	if !mounter.undo || len(systemctl.calls) != 2 {
		t.Fatalf("cleanup was not repeated safely: %+v %+v", mounter, systemctl.calls)
	}
	if _, err := os.Stat(iso); err != nil {
		t.Fatalf("source ISO removed: %v", err)
	}
	for _, target := range []string{filepath.Join(configRoot, "17.json"), filepath.Join(mountRoot, "17"), filepath.Join(assetRoot, "17")} {
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("asset remains %s: %v", target, err)
		}
	}
}

func TestRemoveWindowsAnswerRejectsSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "4")); err != nil {
		t.Fatal(err)
	}
	m := &WindowsISOManager{AnswerRoot: root}
	if err := m.RemoveAnswer(context.Background(), 4); err == nil {
		t.Fatal("removed symlink target")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	}
}
