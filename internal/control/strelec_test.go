package control

import (
	"context"
	"net/http"
	"net/url"
	"omb/internal/agent"
	"strconv"
	"strings"
	"testing"
)

func TestStrelecISOCanBeChosenAsServiceBootSource(t *testing.T) {
	s, store, prep := downloadsServer(t)
	api := s.Images.(*fakeImagesAPI)
	api.catalog.Images[1].Path = "WinPE11_10_Sergei_Strelec_x64.iso"
	body := libraryGet(t, s, "/installations/new?storage_id=7&path=WinPE11_10_Sergei_Strelec_x64.iso")
	if strings.Contains(body, "Сервисные образы WinPE пока не поддерживаются") {
		t.Fatal("Strelec is still rejected as unsupported")
	}
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:WinPE11_10_Sergei_Strelec_x64.iso"}, "title": {"Strelec"}, "folder": {"Tools"}})
	if res.Code != http.StatusSeeOther || len(store.installations) != 1 || prep.calls != 1 {
		t.Fatalf("status=%d entries=%+v preparations=%d", res.Code, store.installations, prep.calls)
	}
	if store.bootProfiles[len(store.bootProfiles)-1].Kind != BootProfileWinPEISO || prep.req.BootMode != agent.ISOStrelecMode || store.profiles[len(store.profiles)-1].State != "ready" {
		t.Fatalf("wrong service mode: profiles=%+v request=%+v", store.profiles, prep.req)
	}
}

func TestStrelecKindIsNotRejectedByFilename(t *testing.T) {
	kind, problem := isoKindHint("WinPE11_10_Sergei_Strelec_x64_2026.10.21_Russian.iso")
	if kind != "WinPE Strelec" || problem != "" {
		t.Fatalf("kind=%q problem=%q", kind, problem)
	}
}

func TestStrelecMenuInjectsNativePXEAndNeverWindowsSetup(t *testing.T) {
	s, store, _ := downloadsServer(t)
	s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "Strelec.iso"
	res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Strelec.iso"}, "title": {"Strelec"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("add: %d", res.Code)
	}
	sources, err := s.installationMenuSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	script, err := BuildMenuFromDraft(*store.draft, testBootURL, store.profiles, store.answers, store.configs, sources)
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, want := range []string{"/assets/windows/wimboot", "/windows-profiles/80/boot.wim boot.wim", "/windows-profiles/80/pecmdmain.ini pecmdmain.ini", "/windows-profiles/80/omb-setup.cmd network_pxe.cmd", "initrd -n network_pxe.cmd", "/windows-profiles/80/bootmgr bootmgr", "/windows-profiles/80/bootx64.efi bootx64.efi", "/windows-profiles/80/BCD BCD", "/windows-profiles/80/boot.sdi boot.sdi"} {
		if !strings.Contains(text, want) {
			t.Fatalf("service menu lacks %q: %s", want, text)
		}
	}
	if strings.Contains(text, "winpeshl.ini") || strings.Contains(text, "omb-answer.xml") {
		t.Fatal("service menu replaced native Strelec startup")
	}
}

func TestStrelecCreationRejectsInstallerOptionsBeforeSaving(t *testing.T) {
	for _, field := range []string{"answer_id", "autoinstall_id"} {
		s, store, prep := downloadsServer(t)
		s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "Strelec.iso"
		res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Strelec.iso"}, "title": {"Strelec"}, field: {"31"}})
		if res.Code != http.StatusBadRequest || len(store.installations) != 0 || store.sourceCreates != 0 || prep.calls != 0 {
			t.Fatalf("field=%s status=%d sourceCreates=%d", field, res.Code, store.sourceCreates)
		}
	}
}

func TestStrelecCanBePublishedAndRestoredWithSourceProtection(t *testing.T) {
	s, store, _ := downloadsServer(t)
	s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "Strelec.iso"
	if res := menuPost(t, s, "/installations", url.Values{"boot_profile_id": {"iso:7:Strelec.iso"}, "title": {"Strelec"}}); res.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", res.Code)
	}
	if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("add: %d", res.Code)
	}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish: %d %s", res.Code, res.Body.String())
	}
	version := store.versions[len(store.versions)-1]
	if !strings.Contains(version.Script, "network_pxe.cmd") {
		t.Fatal("published menu lost native PXE startup")
	}
	if err := s.verifyMenuVersion(context.Background(), version); err != nil {
		t.Fatalf("cannot restore service source: %v", err)
	}
	used, err := s.activeMenuContains(accountRequest(http.MethodGet, "/images", nil), installationComponent{profileID: 80})
	if err != nil || !used {
		t.Fatalf("active service source lost protection: %v %v", used, err)
	}
	body := libraryGet(t, s, "/installations/100/edit")
	if !strings.Contains(body, `id="windows-answers" hidden disabled`) || strings.Contains(body, `id="windows-drivers" hidden disabled`) {
		t.Fatal("service editor shows Windows installer options")
	}
}

func TestStrelecDriversUseSameDownloadWorkflowAndDerivedMenu(t *testing.T) {
	for _, selection := range []string{"inf", "package"} {
		t.Run(selection, func(t *testing.T) {
			s, store, _ := downloadsServer(t)
			s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "Strelec.iso"
			builder := &fakeDriverBuilder{}
			s.DriverConfigs = builder
			s.DriverPackages = &recordingPackageCheck{}
			form := url.Values{"boot_profile_id": {"iso:7:Strelec.iso"}, "title": {"Strelec VirtIO"}, "folder": {"Tools"}}
			value := strings.Repeat("a", 64)
			if selection == "inf" {
				value += ":NetKVM/netkvm.inf"
			}
			form.Set(selection, value)
			res := menuPost(t, s, "/installations", form)
			if res.Code != http.StatusSeeOther || len(store.created) != 1 || len(builder.calls) != 1 {
				t.Fatalf("status=%d configs=%+v builds=%+v body=%s", res.Code, store.created, builder.calls, res.Body.String())
			}
			entry := store.installations[0]
			if entry.DriverConfigID == 0 || builder.calls[0].ProfileID != 80 || builder.calls[0].AnswerID != 0 {
				t.Fatal("wrong service driver binding")
			}
			if res := menuPost(t, s, "/installations/100/add", url.Values{}); res.Code != http.StatusSeeOther {
				t.Fatalf("add: %d", res.Code)
			}
			sources, err := s.installationMenuSources(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			script, err := BuildMenuFromDraft(*store.draft, testBootURL, store.profiles, store.answers, store.configs, sources)
			if err != nil {
				t.Fatal(err)
			}
			text := string(script)
			for _, want := range []string{"/windows-driver-wims/", "/windows-driver-configs/40/omb-setup.cmd network_pxe.cmd", "initrd -n network_pxe.cmd", "/windows-profiles/80/BCD BCD", "pecmdmain.ini"} {
				if !strings.Contains(text, want) {
					t.Fatalf("service menu lacks %s", want)
				}
			}
			if strings.Contains(text, "winpeshl.ini") || strings.Contains(text, "omb-answer.xml") || strings.Contains(text, "/windows-profiles/80/boot.wim") {
				t.Fatal("derived service menu uses installer or base WIM")
			}
			if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
				t.Fatalf("publish: %d %s", res.Code, res.Body.String())
			}
			if err := s.verifyMenuVersion(context.Background(), store.versions[len(store.versions)-1]); err != nil {
				t.Fatal(err)
			}
			body := libraryGet(t, s, "/installations/100/edit")
			if !strings.Contains(body, `name="inf" value="`+strings.Repeat("a", 64)+`:NetKVM/netkvm.inf" checked`) {
				t.Fatal("driver selection lost on edit")
			}
			active := store.versions[len(store.versions)-1]
			edit := url.Values{"boot_profile_id": {"81"}, "title": {"Strelec VirtIO"}, "folder": {"Tools"}, "inf": {strings.Repeat("a", 64) + ":vioscsi/vioscsi.inf"}}
			res = menuPost(t, s, "/installations/100", edit)
			if res.Code != http.StatusSeeOther || len(builder.calls) != 2 {
				t.Fatalf("edit: %d builds=%d", res.Code, len(builder.calls))
			}
			if store.versions[len(store.versions)-1].Items[0].Components.DriverConfigID != active.Items[0].Components.DriverConfigID || store.draft.Items[0].Components.DriverConfigID == active.Items[0].Components.DriverConfigID {
				t.Fatal("driver edit changed active snapshot or lost pending binding")
			}
			if res := menuPost(t, s, "/installations/100/prepare", url.Values{}); res.Code != http.StatusSeeOther || len(builder.calls) != 2 {
				t.Fatal("ready service driver retry rebuilt the config")
			}
		})
	}
}

func TestServiceDriverSelectionRejectsInstallerAnswer(t *testing.T) {
	_, store, _ := downloadsServer(t)
	source := store.profiles[0]
	source.BootMode = agent.ISOStrelecMode
	source.InstallFile = ""
	source.State = "ready"
	form := url.Values{"profile_id": {strconv.FormatInt(source.ID, 10)}, "title": {"Strelec"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}}
	if _, err := SelectDriverConfig(form, []WindowsProfile{source}, nil, store.packages); err != nil {
		t.Fatal(err)
	}
	form.Set("answer_id", "31")
	if _, err := SelectDriverConfig(form, []WindowsProfile{source}, store.answers, store.packages); err == nil {
		t.Fatal("service config accepted installer XML")
	}
}

func TestStrelecSelectionReusesNewestPreparedDriverConfiguration(t *testing.T) {
	s, store, _ := downloadsServer(t)
	s.Images.(*fakeImagesAPI).catalog.Images[1].Path = "Strelec.iso"
	s.DriverConfigs = &fakeDriverBuilder{}
	form := url.Values{"boot_profile_id": {"iso:7:Strelec.iso"}, "title": {"Strelec"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}}
	if r := menuPost(t, s, "/installations", form); r.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", r.Code)
	}
	newer := store.configs[len(store.configs)-1]
	newer.ID += 1
	store.configs = append(store.configs, newer)
	form = url.Values{"boot_profile_id": {"81"}, "title": {"Strelec second"}, "inf": {strings.Repeat("a", 64) + ":NetKVM/netkvm.inf"}}
	if r := menuPost(t, s, "/installations", form); r.Code != http.StatusSeeOther {
		t.Fatalf("create: %d", r.Code)
	}
	if store.installations[1].DriverConfigID != newer.ID {
		t.Fatal("new entry reused the superseded startup script")
	}
}
