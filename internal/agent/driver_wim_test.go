package agent

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type fakeDriverRunner struct {
	calls []string
	fail  string
}

func (f *fakeDriverRunner) Run(_ context.Context, input, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != "" && strings.Contains(call, f.fail) {
		return nil, errors.New("simulated wimlib failure")
	}
	if strings.Contains(call, " info ") {
		return []byte("Image Count: 2\nBoot Index: 2\n"), nil
	}
	return nil, nil
}

func setupDriverBuild(t *testing.T) (*DriverManager, string) {
	t.Helper()
	m := &DriverManager{Root: t.TempDir(), ProfileRoot: t.TempDir(), AnswerRoot: t.TempDir(), WIMRoot: t.TempDir(), ConfigRoot: t.TempDir(), Runner: &fakeDriverRunner{}}
	parent := filepath.Join(m.ProfileRoot, "17")
	if err := os.Mkdir(parent, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "boot.wim"), []byte("fake source WIM"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "pxe-setup.cmd"), []byte("@echo off\r\nwpeinit\r\nY:\\17\\sources\\setup.exe\r\n:failed\r\ncmd /k\r\n"), 0644); err != nil {
		t.Fatal(err)
	}
	archive := driverArchive(t)
	inventory, err := m.Import(context.Background(), bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	return m, inventory.SHA256
}

func TestDriverWIMReusesIdenticalSelection(t *testing.T) {
	m, hash := setupDriverBuild(t)
	req := DriverBuildRequest{ConfigID: 1, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
	first, err := m.PrepareConfig(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.ConfigID = 2
	second, err := m.PrepareConfig(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.WIMKey != second.WIMKey || first.WIMSHA256 != second.WIMSHA256 {
		t.Fatalf("cache not reused: %+v %+v", first, second)
	}
	if err := m.CheckConfig(context.Background(), req, second); err != nil {
		t.Fatal(err)
	}
	updates := 0
	for _, call := range m.Runner.(*fakeDriverRunner).calls {
		if strings.Contains(call, " update ") {
			updates++
		}
	}
	if updates != 1 {
		t.Fatalf("expected one WIM build, got %d", updates)
	}
}

func TestDriverWIMFailureLeavesNoPublishedConfig(t *testing.T) {
	m, hash := setupDriverBuild(t)
	m.Runner = &fakeDriverRunner{fail: " update "}
	req := DriverBuildRequest{ConfigID: 3, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
	if _, err := m.PrepareConfig(context.Background(), req); err == nil {
		t.Fatal("accepted failed wimlib update")
	}
	items, err := os.ReadDir(m.WIMRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("failed WIM was published: %v", items)
	}
	if _, err := os.Stat(filepath.Join(m.ConfigRoot, "3")); !os.IsNotExist(err) {
		t.Fatalf("failed config was published: %v", err)
	}
}

func TestDriverWIMRejectsLowFreeSpace(t *testing.T) {
	m, hash := setupDriverBuild(t)
	m.FreeBytes = func(string) (uint64, error) { return 0, nil }
	req := DriverBuildRequest{ConfigID: 4, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
	if _, err := m.PrepareConfig(context.Background(), req); err == nil {
		t.Fatal("accepted insufficient space")
	}
}

func TestDriverWIMRejectsTamperedPackage(t *testing.T) {
	m, hash := setupDriverBuild(t)
	if err := os.WriteFile(filepath.Join(m.Root, hash, "files", "net.sys"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	req := DriverBuildRequest{ConfigID: 5, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
	if _, err := m.PrepareConfig(context.Background(), req); err == nil {
		t.Fatal("accepted tampered package")
	}
}

func TestDriverWIMSelectionChangesCacheKey(t *testing.T) {
	m, firstHash := setupDriverBuild(t)
	var data bytes.Buffer
	zw := zip.NewWriter(&data)
	inf, err := zw.Create("storage.inf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inf.Write([]byte("[Version]\nClass=SCSIAdapter\n[Models.NTamd64]\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := m.Import(context.Background(), bytes.NewReader(data.Bytes()), int64(data.Len()))
	if err != nil {
		t.Fatal(err)
	}
	base := DriverBuildRequest{ConfigID: 6, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: firstHash, INFPath: "net.inf"}}}
	a, err := m.PrepareConfig(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	base.ConfigID = 7
	base.PackageINFs = []SelectedINF{{PackageSHA256: second.SHA256, INFPath: "storage.inf"}}
	b, err := m.PrepareConfig(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if a.WIMKey == b.WIMKey {
		t.Fatal("different selections reused one WIM")
	}
}

func TestDriverWIMCheckRejectsCorruptedOutput(t *testing.T) {
	m, hash := setupDriverBuild(t)
	req := DriverBuildRequest{ConfigID: 8, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
	result, err := m.PrepareConfig(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.WIMRoot, result.WIMKey, "boot.wim"), []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckConfig(context.Background(), req, result); err == nil {
		t.Fatal("accepted corrupted derived WIM")
	}
}

// Configurations built before the WinPE messages became "OMB:" carry
// "PXE Platform:" or "Oh My Boot:"; publishing must still accept them until
// they are rebuilt, and a fresh build uses "OMB:".
func TestDriverWIMCheckAcceptsScriptBuiltBeforeRename(t *testing.T) {
	for i, old := range []string{"PXE Platform", "Oh My Boot"} {
		m, hash := setupDriverBuild(t)
		id := int64(20 + i)
		req := DriverBuildRequest{ConfigID: id, ProfileID: 17, PackageINFs: []SelectedINF{{PackageSHA256: hash, INFPath: "net.inf"}}}
		result, err := m.PrepareConfig(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		final := filepath.Join(m.ConfigRoot, strconv.FormatInt(id, 10))
		current, err := os.ReadFile(filepath.Join(final, "pxe-setup.cmd"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(current, []byte("echo OMB: loading WinPE drivers...")) {
			t.Fatalf("a fresh build does not use OMB: %q", current)
		}
		legacy := bytes.ReplaceAll(current, []byte("echo OMB: "), []byte("echo "+old+": "))
		sum := sha256.Sum256(legacy)
		result.ScriptSHA256 = hex.EncodeToString(sum[:])
		manifest, err := json.Marshal(driverConfigManifest{Request: req, Result: result})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(final, "build.json"), manifest, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(final, "pxe-setup.cmd"), legacy, 0644); err != nil {
			t.Fatal(err)
		}
		if err := m.CheckConfig(context.Background(), req, result); err != nil {
			t.Fatalf("%s: configuration built before the rename was rejected: %v", old, err)
		}
		// Any other change to the script is still caught.
		if err := os.WriteFile(filepath.Join(final, "pxe-setup.cmd"), append(legacy, []byte("rem\r\n")...), 0644); err != nil {
			t.Fatal(err)
		}
		if err := m.CheckConfig(context.Background(), req, result); err == nil {
			t.Fatalf("%s: accepted a changed WinPE script", old)
		}
	}
}
