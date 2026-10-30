package control

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"pxe-platform/internal/boot"
)

type autoinstallMenuStore struct {
	*unifiedMenuStore
	files       []UbuntuAutoinstallFile
	fetched     []int64
	failSave    bool
	missingBody bool
}

func (s *autoinstallMenuStore) UbuntuAutoinstalls(context.Context) ([]UbuntuAutoinstallSummary, error) {
	var out []UbuntuAutoinstallSummary
	for _, f := range s.files {
		out = append(out, UbuntuAutoinstallSummary{ID: f.ID, Title: f.Title, SHA256: f.SHA256, State: f.State})
	}
	return out, nil
}
func (s *autoinstallMenuStore) UbuntuAutoinstall(_ context.Context, id int64) (UbuntuAutoinstallFile, error) {
	s.fetched = append(s.fetched, id)
	if s.missingBody {
		return UbuntuAutoinstallFile{}, errors.New("missing body")
	}
	for _, f := range s.files {
		if f.ID == id {
			return f, nil
		}
	}
	return UbuntuAutoinstallFile{}, errors.New("missing library file")
}
func (s *autoinstallMenuStore) SaveActiveMenu(ctx context.Context, user int64, m MenuVersion) error {
	if s.failSave {
		return errors.New("database unavailable")
	}
	return s.draftStore.SaveActiveMenu(ctx, user, m)
}

type autoinstallMenuBoot struct {
	root      string
	publisher *boot.Publisher
	assets    *boot.AutoinstallAssets
	public    http.Handler
	calls     []string
	fail      string
}

func autoinstallMenuFixture(t *testing.T) (*Server, *autoinstallMenuStore, *autoinstallMenuBoot) {
	t.Helper()
	s, base, _ := unifiedMenuFixture(t)
	first, second, unused := testAutoinstallFile(t, "lab-one"), testAutoinstallFile(t, "lab-two"), testAutoinstallFile(t, "unused")
	first.ID, second.ID, unused.ID = 11, 12, 13
	store := &autoinstallMenuStore{unifiedMenuStore: base, files: []UbuntuAutoinstallFile{first, second, unused}}
	store.installations[0].AutoinstallID = first.ID
	store.installations[2].AutoinstallID = second.ID
	store.installations = append(store.installations, Installation{ID: 74, BootProfileID: 1, Title: "Ubuntu Manual"}, Installation{ID: 75, BootProfileID: 2, Title: "Windows XML", AnswerID: 31})
	store.draft.Items = append(store.draft.Items, MenuDraftItem{InstallationID: 74}, MenuDraftItem{InstallationID: 75})
	s.Store = store
	h := &autoinstallMenuBoot{root: t.TempDir()}
	var err error
	h.assets, err = boot.NewAutoinstallAssets(h.root)
	if err != nil {
		t.Fatal(err)
	}
	h.publisher, err = boot.NewPublisher(filepath.Join(h.root, "menu.ipxe"), []byte("#!ipxe\n# previous menu\n"))
	if err != nil {
		t.Fatal(err)
	}
	h.public, err = boot.NewDynamicHandler(h.root, h.publisher, nil)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "boot.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	control := boot.ControlHandler(h.publisher, nil, h.assets)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + r.URL.Path
		h.calls = append(h.calls, call)
		if h.fail != "" && strings.HasPrefix(call, h.fail) {
			http.Error(w, "deliberate failure", http.StatusServiceUnavailable)
			return
		}
		control.ServeHTTP(w, r)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	s.Boot = NewBootClient(socket)
	return s, store, h
}

func seedPath(id int64, file UbuntuAutoinstallFile) string {
	return fmt.Sprintf("/assets/ubuntu-autoinstall/%d/%s/", id, file.SHA256)
}

func installationScript(t *testing.T, script string, id int64) string {
	t.Helper()
	start := strings.Index(script, fmt.Sprintf(":install_%d\n", id))
	if start < 0 {
		t.Fatalf("missing installation %d", id)
	}
	block := script[start:]
	if end := strings.Index(block, "goto local\n"); end >= 0 {
		block = block[:end+len("goto local\n")]
	}
	return block
}

func TestUbuntuAutoinstallMixedMenuStagesSelectedFilesBeforePublish(t *testing.T) {
	s, store, h := autoinstallMenuFixture(t)
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
	script, _ := h.publisher.Current()
	for _, id := range []int64{71, 72, 73, 74, 75} {
		block := installationScript(t, string(script), id)
		report := fmt.Sprintf(":install_%d\nimgfetch --name pxe-choice %s/boot/choice?id=%d && imgfree pxe-choice || echo -n\nkernel ", id, s.BootBaseURL, id)
		if strings.Count(string(script), report) != 1 {
			t.Errorf("choice %d is not unique and before files", id)
		}
		if id != 71 && id != 73 && strings.Contains(block, "ds=nocloud-net") {
			t.Errorf("manual/Windows installation %d received NoCloud", id)
		}
	}
	for i, id := range []int64{71, 73} {
		block := installationScript(t, string(script), id)
		t.Logf("automated Ubuntu installation %d:\n%s", id, block)
		seed := s.BootBaseURL + seedPath(id, store.files[i])
		want := "kernel " + s.BootBaseURL + "/assets/ubuntu/vmlinuz initrd=initrd boot=casper ip=dhcp iso-url=" + s.BootBaseURL + "/images/ubuntu-desktop.iso autoinstall ds=nocloud-net;s=" + seed + " ---\n"
		if !strings.Contains(block, want) {
			t.Errorf("wrong kernel argument for %d:\n%s", id, block)
		}
		for _, want := range []string{"initrd " + s.BootBaseURL + "/assets/ubuntu/initrd\n", "shim " + s.BootBaseURL + "/assets/ubuntu/bootx64.efi"} {
			if !strings.Contains(block, want) {
				t.Errorf("Ubuntu source changed: missing %s", want)
			}
		}
		assertSeedAvailable(t, h, id, store.files[i])
	}
	for _, want := range []string{"kernel " + s.BootBaseURL + "/assets/windows/wimboot\n", "/assets/windows-profiles/17/boot.wim boot.wim\n", "/assets/windows-answers/31/pxe-answer.xml"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("Windows path changed: %s", want)
		}
	}
	if !reflect.DeepEqual(store.fetched, []int64{11, 12}) {
		t.Errorf("fetched unselected files: %v", store.fetched)
	}
	wantCalls := []string{"PUT /autoinstall/71/" + store.files[0].SHA256, "GET /autoinstall/71/" + store.files[0].SHA256, "PUT /autoinstall/73/" + store.files[1].SHA256, "GET /autoinstall/73/" + store.files[1].SHA256, "GET /menu", "POST /publish"}
	if !reflect.DeepEqual(h.calls, wantCalls) {
		t.Errorf("preflight ordering=%v want=%v", h.calls, wantCalls)
	}
	if len(store.versions) != 1 || len(diffMenuDraft(draftFromVersion(store.versions[0]), *store.draft)) != 0 {
		t.Fatal("published structure/draft inconsistent")
	}
}

func assertSeedAvailable(t *testing.T, h *autoinstallMenuBoot, id int64, f UbuntuAutoinstallFile) {
	t.Helper()
	for _, name := range []string{"user-data", "meta-data"} {
		r := httptest.NewRequest(http.MethodGet, seedPath(id, f)+name, nil)
		r.RemoteAddr = "10.30.0.25:1234"
		w := httptest.NewRecorder()
		h.public.ServeHTTP(w, r)
		want := string(f.UserData)
		if name == "meta-data" {
			want = fmt.Sprintf("instance-id: pxe-installation-%d-%s\n", id, f.SHA256)
		}
		if w.Code != http.StatusOK || w.Body.String() != want || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("seed %d/%s status=%d body=%s", id, name, w.Code, w.Body.String())
		}
	}
}

func TestUbuntuAutoinstallPreflightFailuresPreserveMenuVersionAndDraft(t *testing.T) {
	for _, action := range []string{"publish", "rollback"} {
		for _, failure := range []string{"stage", "check", "missing library", "missing body", "invalid body", "database", "snapshot digest"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				s, store, h := autoinstallMenuFixture(t)
				store.draft.Items = store.draft.Items[:1]
				sources, err := s.installationMenuSources(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				script, err := BuildMenuFromDraft(*store.draft, s.BootBaseURL, store.profiles, store.answers, store.configs, sources)
				if err != nil {
					t.Fatal(err)
				}
				store.versions = []MenuVersion{{ID: 9, Active: true, Title: productMenuTitle, Label: store.draft.Label, Message: store.draft.Message, Script: string(script), Items: versionItems(*store.draft)}}
				beforeVersions := append([]MenuVersion(nil), store.versions...)
				beforeDraft := *store.draft
				beforeScript, _ := h.publisher.Current()
				switch failure {
				case "stage":
					h.fail = "PUT /autoinstall/"
				case "check":
					h.fail = "GET /autoinstall/"
				case "missing library":
					store.files = nil
				case "invalid body":
					store.files[0].UserData = []byte("corrupted")
				case "missing body":
					store.missingBody = true
				case "database":
					store.failSave = true
				case "snapshot digest":
					if action == "publish" {
						t.Skip("only restoration uses a saved snapshot")
					}
					store.versions[0].Script = strings.ReplaceAll(store.versions[0].Script, store.files[0].SHA256, strings.Repeat("a", 64))
					beforeVersions = append([]MenuVersion(nil), store.versions...)
				}
				res := menuPost(t, s, "/menu/"+action, url.Values{"id": {"9"}})
				current, _ := h.publisher.Current()
				if res.Code < 400 || string(current) != string(beforeScript) || !reflect.DeepEqual(store.versions, beforeVersions) || !reflect.DeepEqual(*store.draft, beforeDraft) {
					t.Fatalf("failure=%s status=%d calls=%v changed menu/version/draft", failure, res.Code, h.calls)
				}
				if failure != "database" && strings.Contains(strings.Join(h.calls, "\n"), "POST /publish") {
					t.Fatal("published before preflight succeeded")
				}
			})
		}
	}
}

func TestUbuntuAutoinstallReusedFileKeepsSeparateInstallationIdentities(t *testing.T) {
	s, store, h := autoinstallMenuFixture(t)
	store.installations[2].AutoinstallID = store.files[0].ID
	store.draft.Items = []MenuDraftItem{{InstallationID: 71}, {InstallationID: 73}}
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
	script, _ := h.publisher.Current()
	for _, id := range []int64{71, 73} {
		if !strings.Contains(installationScript(t, string(script), id), seedPath(id, store.files[0])) {
			t.Errorf("shared file lost installation identity %d", id)
		}
		assertSeedAvailable(t, h, id, store.files[0])
	}
}

func TestUbuntuAutoinstallRestoreRetainsOldSeedAndRejectsCorruption(t *testing.T) {
	s, store, h := autoinstallMenuFixture(t)
	store.draft.Items = store.draft.Items[:1]
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
	old := store.versions[0]
	store.draft.Items = []MenuDraftItem{{InstallationID: 73}}
	if res := menuPost(t, s, "/menu/publish", url.Values{}); res.Code != http.StatusSeeOther {
		t.Fatalf("publish=%d %s", res.Code, res.Body.String())
	}
	res := menuPost(t, s, "/menu/rollback", url.Values{"id": {fmt.Sprint(old.ID)}})
	current, _ := h.publisher.Current()
	if res.Code != http.StatusSeeOther || string(current) != old.Script || !strings.Contains(string(current), seedPath(71, store.files[0])) {
		t.Fatalf("restore=%d %s", res.Code, res.Body.String())
	}
	assertSeedAvailable(t, h, 71, store.files[0])
	assertSeedAvailable(t, h, 73, store.files[1])
	// Existing damaged immutable pairs must block restoration rather than be overwritten.
	path := filepath.Join(h.root, "ubuntu-autoinstall", "71", store.files[0].SHA256, "meta-data")
	if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	beforeVersions := append([]MenuVersion(nil), store.versions...)
	beforeDraft := *store.draft
	res = menuPost(t, s, "/menu/rollback", url.Values{"id": {fmt.Sprint(old.ID)}})
	after, _ := h.publisher.Current()
	if res.Code < 400 || string(after) != string(current) || !reflect.DeepEqual(store.versions, beforeVersions) || !reflect.DeepEqual(*store.draft, beforeDraft) {
		t.Fatalf("corrupt seed restoration=%d", res.Code)
	}
}
