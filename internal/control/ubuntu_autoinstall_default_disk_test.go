package control

import (
	"strings"
	"testing"

	"omb/internal/autoinstall"
)

func defaultDiskAutoinstallFile(t *testing.T, hostname string) UbuntuAutoinstallFile {
	t.Helper()
	file := testAutoinstallFile(t, hostname)
	file.Values.DiskMode = "default"
	file.Values.DiskSerial = ""
	data, err := autoinstall.Render(file.Values)
	if err != nil {
		t.Fatal(err)
	}
	file.UserData = data
	file.SHA256 = fmtDigest(data)
	return file
}

func TestUbuntuDefaultDiskWarningsBeyondEditor(t *testing.T) {
	s, store := autoinstallServer(t)
	file := defaultDiskAutoinstallFile(t, "default-disk")
	file.ID = 1
	store.files = []UbuntuAutoinstallFile{file}

	library := libraryGet(t, s, "/answers")
	if !strings.Contains(library, "Стирает диск") || !strings.Contains(library, "Диск выбирает установщик") {
		t.Fatalf("library hides destructive default mode: %s", library)
	}
	selector := libraryGet(t, s, "/installations/new?boot_profile_id=52")
	for _, want := range []string{`data-wipes="true"`, "стирает диск: выбор установщика"} {
		if !strings.Contains(selector, want) {
			t.Fatalf("installation selector missing %q: %s", want, selector)
		}
	}

	sources := InstallationMenuSources{
		Installations: []Installation{{ID: 77, BootProfileID: 52, Title: "Ubuntu", AutoinstallID: 1}},
		BootProfiles:  []BootProfile{{ID: 52, Kind: BootProfileUbuntuBuiltin}},
		Autoinstalls:  []UbuntuAutoinstallSummary{{ID: 1, State: "validated", SHA256: file.SHA256}},
	}
	for _, tc := range []struct {
		mode  string
		wipes bool
	}{{"interactive", false}, {"default", true}, {"serial", true}} {
		t.Run(tc.mode, func(t *testing.T) {
			sources.Autoinstalls[0].DiskMode = tc.mode
			rows := installationRows(nil, nil, nil, MenuDraft{}, MenuDraft{}, sources)
			if len(rows) != 1 || rows[0].AnswerWipesDisk == nil || *rows[0].AnswerWipesDisk != tc.wipes {
				t.Fatalf("installation row wipes=%+v, want %t", rows, tc.wipes)
			}
			entry, _, err := menuProfileForInstallation(77, "", sources, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := entry.hint == "!! WIPES DISK"; got != tc.wipes {
				t.Fatalf("menu hint=%q, wipes=%t", entry.hint, got)
			}
		})
	}
}

func TestUbuntuEditorDiskWarningsMatchMode(t *testing.T) {
	for _, mode := range []string{"interactive", "default", "serial"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := autoinstallServer(t)
			form := constructorForm()
			form.Set("disk_mode", mode)
			if mode == "serial" {
				form.Set("disk_serial", "DISK123")
			}
			body := constructorPost(t, s, "/ubuntu-autoinstall/preview", form).Body.String()
			checks := map[string]bool{
				`<p class="ed-warn" id="ai-default-disk-warning">`:        mode == "default",
				`<p class="ed-warn" id="ai-default-disk-warning" hidden>`: mode != "default",
				`<p class="ed-warn" id="ai-serial-disk-warning">`:         mode == "serial",
				`<p class="ed-warn" id="ai-serial-disk-warning" hidden>`:  mode != "serial",
			}
			for want, present := range checks {
				if strings.Contains(body, want) != present {
					t.Fatalf("warning %q presence=%t, want %t: %s", want, !present, present, body)
				}
			}
		})
	}
}
