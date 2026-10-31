package ombmigration

import (
	"archive/zip"
	"bytes"
	"context"
	"omb/internal/agent"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRuntimeRebuildDoesNotReuseLegacyDriverWIMCache(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Join(root, "stage")
	os.Mkdir(stage, 0700)
	os.MkdirAll(filepath.Join(root, "mnt/omb-smb"), 0755)
	write := func(p string, b []byte) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	template, err := os.ReadFile("../../deploy/boot-assets/windows-profiles/omb-setup.cmd.in")
	if err != nil {
		t.Fatal(err)
	}
	write("usr/share/omb/windows-profiles/omb-setup.cmd.in", template)
	write("usr/share/omb/windows-profiles/winpeshl.ini", []byte("shell"))
	write("etc/omb/omb-iso-password", []byte(strings.Repeat("A", 48)))
	write("var/lib/omb-images/iso/unchanged-pxe.iso", []byte("ISO"))
	isoInfo, _ := os.Stat(filepath.Join(root, "var/lib/omb-images/iso/unchanged-pxe.iso"))
	for _, name := range []string{"efi/boot/bootx64.efi", "sources/setup.exe", "sources/boot.wim", "sources/install.wim"} {
		write("mnt/omb-iso/profiles/1/"+name, []byte("original media"))
	}
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	f, _ := z.Create("net.inf")
	f.Write([]byte("[Version]\nSignature=\"$Windows NT$\"\nClass=Net\nProvider=Lab\n[Manufacturer]\nLab=Models,NTamd64\n[Models.NTamd64]\nDevice=Install,PCI\\VEN_1234&DEV_5678\n"))
	z.Close()
	driverRoot := filepath.Join(root, "var/lib/omb-agent/drivers")
	os.MkdirAll(driverRoot, 0755)
	packages := &agent.DriverManager{Root: driverRoot}
	inventory, err := packages.Import(context.Background(), bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	selected := []agent.SelectedINF{{PackageSHA256: inventory.SHA256, INFPath: "net.inf"}}
	key := hashBytes([]byte(hashBytes([]byte("original media")) + "\x00" + inventory.SHA256 + "\x00net.inf\x00"))
	oldWIM := []byte("legacy cached WIM with pxe-drivers")
	write("stage/windows-driver-wims/"+key+"/boot.wim", oldWIM)
	write("stage/windows-driver-wims/"+key+"/sha256", []byte(hashBytes(oldWIM)))
	unused := strings.Repeat("c", 64)
	write("stage/windows-driver-wims/"+unused+"/boot.wim", oldWIM)
	write("stage/windows-driver-wims/"+unused+"/sha256", []byte(hashBytes(oldWIM)))
	updates := 0
	runner := RunFunc(func(_ context.Context, name string, args []string, input []byte) ([]byte, error) {
		if name == "mountpoint" {
			return nil, nil
		}
		switch args[0] {
		case "info":
			return []byte("Boot Index: 2\n"), nil
		case "update":
			updates++
			if !bytes.Contains(input, []byte("/Windows/System32/omb-drivers/")) {
				t.Fatal("legacy driver injection path")
			}
			f, e := os.OpenFile(args[1], os.O_APPEND|os.O_WRONLY, 0)
			if e != nil {
				return nil, e
			}
			_, e = f.WriteString(" OMB drivers")
			f.Close()
			return nil, e
		}
		return nil, nil
	})
	xml := []byte(`<unattend/>`)
	source := Snapshot{"pxe_control": {"windows_profiles": {{"id": int64(1), "state": "ready", "install_file": "install.wim"}}, "winpe_driver_configs": {{"id": int64(4), "selected_infs": selected}}}}
	m := Manifest{Profiles: []Object{{"id": int64(1)}}, ProfileConfigs: []agent.WindowsISORequest{{ID: 1, StorageRoot: "/var/lib/pxe-platform-images/iso", RelativePath: "unchanged-pxe.iso", Size: 3, Modified: time.Unix(0, isoInfo.ModTime().UnixNano())}}, Answers: []Object{{"id": int64(3), "profile_id": int64(1), "state": "ready", "sha256": hashBytes(xml)}}, Drivers: []Object{{"id": int64(4), "profile_id": int64(1), "answer_id": int64(3), "state": "ready", "wim_key": key, "wim_sha256": hashBytes(oldWIM), "script_sha256": strings.Repeat("a", 64)}}}
	result, err := (RuntimeRebuilder{Runner: runner}).Rebuild(context.Background(), BuildInput{Root: root, Stage: stage, Manifest: m, Source: source, XML: map[int64][]byte{3: xml}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(stage, "windows-driver-wims", unused)); !os.IsNotExist(err) {
		t.Fatal("unreferenced legacy WIM cache could break future configurations")
	}
	if updates != 1 || result.Drivers[4].NewWIM == hashBytes(oldWIM) {
		t.Fatal("reused old WIM containing legacy embedded driver directory")
	}
	if _, err = os.Stat(filepath.Join(stage, "windows-answers/3/omb-answer.xml")); err != nil {
		t.Fatal(err)
	}
}
