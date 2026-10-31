package boot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var autoinstallTestData = []byte("#cloud-config\nautoinstall:\n  version: 1\n")

func autoinstallDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestAutoinstallAssetsStageRetryAndRestart(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := autoinstallDigest(autoinstallTestData)
	if _, err := assets.Check(7, digest); err == nil {
		t.Fatal("missing asset reported available")
	}
	info, err := assets.Stage(7, digest, autoinstallTestData)
	if err != nil || info.UserDataSHA256 != digest {
		t.Fatalf("stage: %+v %v", info, err)
	}
	dir := filepath.Join(root, "ubuntu-autoinstall", "7", digest)
	meta := []byte("instance-id: omb-installation-7-" + digest + "\n")
	for name, want := range map[string][]byte{"user-data": autoinstallTestData, "meta-data": meta} {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if info.MetaDataSHA256 != autoinstallDigest(meta) {
		t.Fatalf("metadata digest: %+v", info)
	}
	before, err := os.Stat(filepath.Join(dir, "user-data"))
	if err != nil {
		t.Fatal(err)
	}
	if retry, err := assets.Stage(7, digest, autoinstallTestData); err != nil || retry != info {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	after, _ := os.Stat(filepath.Join(dir, "user-data"))
	if !os.SameFile(before, after) {
		t.Fatal("retry replaced immutable file")
	}
	restarted, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	if checked, err := restarted.Check(7, digest); err != nil || checked != info {
		t.Fatalf("restart: %+v %v", checked, err)
	}
	if _, err := assets.Stage(8, digest, autoinstallTestData); err != nil {
		t.Fatal(err)
	}
	otherMeta, _ := os.ReadFile(filepath.Join(root, "ubuntu-autoinstall", "8", digest, "meta-data"))
	if bytes.Equal(otherMeta, meta) {
		t.Fatal("installations share an instance ID")
	}
}

func TestAutoinstallAssetsRejectInvalidAndConflictingContent(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := autoinstallDigest(autoinstallTestData)
	for _, tc := range []struct {
		id     int64
		digest string
		data   []byte
	}{
		{0, digest, autoinstallTestData}, {-1, digest, autoinstallTestData}, {7, strings.ToUpper(digest), autoinstallTestData}, {7, "../bad", autoinstallTestData},
		{7, strings.Repeat("0", 64), autoinstallTestData}, {7, autoinstallDigest(nil), nil}, {7, autoinstallDigest(bytes.Repeat([]byte("x"), 64<<10+1)), bytes.Repeat([]byte("x"), 64<<10+1)},
	} {
		if _, err := assets.Stage(tc.id, tc.digest, tc.data); err == nil {
			t.Errorf("accepted invalid input id=%d digest=%q size=%d", tc.id, tc.digest, len(tc.data))
		}
	}
	if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
		t.Fatal(err)
	}
	if _, err := assets.Stage(7, digest, []byte("changed")); err == nil {
		t.Fatal("same path different bytes accepted")
	}
	dir := filepath.Join(root, "ubuntu-autoinstall", "7", digest)
	for _, name := range []string{"user-data", "meta-data"} {
		old, _ := os.ReadFile(filepath.Join(dir, name))
		if err := os.WriteFile(filepath.Join(dir, name), []byte("corrupt"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := assets.Check(7, digest); err == nil {
			t.Fatalf("check accepted corrupt %s", name)
		}
		if _, err := assets.Stage(7, digest, autoinstallTestData); err == nil {
			t.Fatalf("stage replaced corrupt %s", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), old, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAutoinstallAssetsRejectSymlinks(t *testing.T) {
	digest := autoinstallDigest(autoinstallTestData)
	for _, component := range []string{"namespace", "id", "version", "user-data", "meta-data"} {
		t.Run(component, func(t *testing.T) {
			root := t.TempDir()
			assets, err := NewAutoinstallAssets(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "ubuntu-autoinstall")
			switch component {
			case "id":
				path = filepath.Join(path, "7")
			case "version":
				path = filepath.Join(path, "7", digest)
			case "user-data", "meta-data":
				path = filepath.Join(path, "7", digest, component)
			}
			target := path + "-moved"
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			if _, err := assets.Check(7, digest); err == nil {
				t.Fatal("check followed symlink")
			}
			if _, err := assets.Stage(7, digest, autoinstallTestData); err == nil {
				t.Fatal("stage followed symlink")
			}
		})
	}
}

func TestAutoinstallAssetsConcurrentStagePublishesCompletePair(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), 64<<10)
	digest := autoinstallDigest(data)
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if _, err := assets.Stage(7, digest, data); err != nil {
				t.Errorf("concurrent stage: %v", err)
			}
		})
	}
	for range 30 {
		for _, name := range []string{"user-data", "meta-data"} {
			req := httptest.NewRequest("GET", "/assets/ubuntu-autoinstall/7/"+digest+"/"+name, nil)
			req.RemoteAddr = "10.30.0.7:1234"
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != 200 && res.Code != 404 {
				t.Fatalf("during stage: %d", res.Code)
			}
			if res.Code == 200 {
				for _, pairName := range []string{"user-data", "meta-data"} {
					if _, err := os.Stat(filepath.Join(root, "ubuntu-autoinstall", "7", digest, pairName)); err != nil {
						t.Fatalf("partial publication: %v", err)
					}
				}
			}
		}
	}
	wg.Wait()
	entries, err := os.ReadDir(filepath.Join(root, "ubuntu-autoinstall", "7"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != digest {
		t.Fatalf("temporary directories left: %v", entries)
	}
}

func TestAutoinstallPublicAssetsPrivacyAndExactPaths(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := autoinstallDigest(autoinstallTestData)
	if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
		t.Fatal(err)
	}
	base := "/assets/ubuntu-autoinstall/7/" + digest + "/"
	// These forbidden files really exist: routing, rather than file absence,
	// must prevent serving an unexpected name or an unfinished staging copy.
	for _, name := range []string{
		"ubuntu-autoinstall/7/" + digest + "/other",
		"ubuntu-autoinstall/7/" + digest + "/user-data.tmp",
		"ubuntu-autoinstall/07/" + digest + "/user-data",
		"ubuntu-autoinstall/7/" + strings.ToUpper(digest) + "/user-data",
		"ubuntu-autoinstall/7/.stage-temp/user-data",
	} {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, autoinstallTestData, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var events []Event
	h, err := NewHandler(root, []byte("#!ipxe\n"), func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user-data", "meta-data"} {
		for _, method := range []string{"GET", "HEAD"} {
			for _, remote := range []string{"10.30.0.7:1234", "192.168.10.7:1234", "[::1]:1234", "invalid"} {
				req := httptest.NewRequest(method, base+name, nil)
				req.RemoteAddr = remote
				res := httptest.NewRecorder()
				h.ServeHTTP(res, req)
				want := 404
				if remote == "10.30.0.7:1234" {
					want = 200
				}
				if res.Code != want {
					t.Errorf("%s %s %s: %d", method, name, remote, res.Code)
				}
				if want == 200 && res.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("private data cacheable")
				}
				if method == "HEAD" && want == 200 && res.Body.Len() != 0 {
					t.Fatal("HEAD returned a body")
				}
			}
		}
	}
	for _, path := range []string{base, base + "other", base + "user-data.tmp", base + "../user-data", base + "%2e%2e/user-data", strings.Replace(base, "/7/", "/07/", 1) + "user-data", strings.Replace(base, digest, strings.ToUpper(digest), 1) + "user-data", "/assets/ubuntu-autoinstall/7/.stage-temp/user-data", base + "/user-data"} {
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "10.30.0.7:1234"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 404 {
			t.Errorf("invalid path %q: %d", path, res.Code)
		}
	}
	for _, method := range []string{"PUT", "POST", "DELETE"} {
		req := httptest.NewRequest(method, base+"user-data", strings.NewReader("changed"))
		req.RemoteAddr = "10.30.0.7:1234"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 405 {
			t.Errorf("public mutation %s: %d", method, res.Code)
		}
	}
	encoded, _ := json.Marshal(events)
	if bytes.Contains(encoded, []byte("#cloud-config")) {
		t.Fatal("event contains user-data")
	}
	// An inside-root symlink must not expose even otherwise valid content.
	dir := filepath.Join(root, "ubuntu-autoinstall", "7", digest)
	if err := os.Rename(filepath.Join(dir, "user-data"), filepath.Join(root, "private")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "private"), filepath.Join(dir, "user-data")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user-data", "meta-data"} {
		req := httptest.NewRequest("GET", base+name, nil)
		req.RemoteAddr = "10.30.0.7:1234"
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != 404 {
			t.Errorf("incomplete/symlink pair %s: %d", name, res.Code)
		}
	}
}

func TestAutoinstallControlAPIBoundsAndDigests(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPublisher(filepath.Join(t.TempDir(), "menu"), []byte("#!ipxe\n"))
	if err != nil {
		t.Fatal(err)
	}
	h := ControlHandler(p, nil, assets)
	digest := autoinstallDigest(autoinstallTestData)
	path := "/autoinstall/7/" + digest
	for _, tc := range []struct {
		method, path string
		data         []byte
		want         int
	}{
		{"GET", path, nil, 404}, {"PUT", path, autoinstallTestData, 200}, {"PUT", path, autoinstallTestData, 200}, {"GET", path, nil, 200},
		{"PUT", path, []byte("changed"), 400}, {"PUT", "/autoinstall/07/" + digest, autoinstallTestData, 400}, {"PUT", "/autoinstall/7/" + strings.Repeat("0", 64), autoinstallTestData, 400},
		{"PUT", path, bytes.Repeat([]byte("x"), 64<<10+1), 413},
	} {
		t.Run(fmt.Sprintf("%s_%d_%d", tc.method, len(tc.data), tc.want), func(t *testing.T) {
			res := httptest.NewRecorder()
			h.ServeHTTP(res, httptest.NewRequest(tc.method, tc.path, bytes.NewReader(tc.data)))
			if res.Code != tc.want {
				t.Fatalf("status %d want %d body=%s", res.Code, tc.want, res.Body.String())
			}
			if tc.want == 200 {
				var info AssetInfo
				if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil || info.UserDataSHA256 != digest || len(info.MetaDataSHA256) != 64 {
					t.Fatalf("info=%+v err=%v", info, err)
				}
			}
		})
	}
	menu, _ := p.Current()
	if string(menu) != "#!ipxe\n" {
		t.Fatal("staging changed menu")
	}
}

func TestAutoinstallPublicAssetsRejectSymlinkAliases(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := autoinstallDigest(autoinstallTestData)
	if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ubuntu-autoinstall", "7", digest, "user-data"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "ubuntu-autoinstall"), filepath.Join(root, "alias-dir")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(root, []byte("#!ipxe\n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/alias", "/assets/alias-dir/7/" + digest + "/user-data"} {
		for _, remote := range []string{"10.30.0.7:1234", "192.168.10.7:1234"} {
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = remote
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != 404 {
				t.Errorf("alias %s from %s: %d", path, remote, res.Code)
			}
		}
	}
}

func TestAutoinstallStageRetrySyncsDirectoryChain(t *testing.T) {
	for _, component := range []string{"version", "installation", "namespace", "root"} {
		t.Run(component, func(t *testing.T) {
			root := t.TempDir()
			assets, err := NewAutoinstallAssets(root)
			if err != nil {
				t.Fatal(err)
			}
			digest := autoinstallDigest(autoinstallTestData)
			if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
				t.Fatal(err)
			}
			target := map[string]string{"version": digest, "installation": "7", "namespace": "ubuntu-autoinstall", "root": assets.root}[component]
			injected := errors.New("injected directory sync failure")
			var synced []string
			assets.syncDirectory = func(dir *os.File) error {
				synced = append(synced, dir.Name())
				if dir.Name() == target {
					return injected
				}
				return dir.Sync()
			}
			if _, err := assets.Stage(7, digest, autoinstallTestData); !errors.Is(err, injected) {
				t.Fatalf("retry skipped %s sync: err=%v synced=%v", component, err, synced)
			}
			assets.syncDirectory = (*os.File).Sync
			if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
				t.Fatalf("recovered retry: %v", err)
			}
		})
	}
}

func TestAutoinstallStageRetryAfterFailedPublicationSync(t *testing.T) {
	root := t.TempDir()
	assets, err := NewAutoinstallAssets(root)
	if err != nil {
		t.Fatal(err)
	}
	digest := autoinstallDigest(autoinstallTestData)
	injected := errors.New("injected publication sync failure")
	assets.syncDirectory = func(dir *os.File) error {
		if dir.Name() == "7" {
			return injected
		}
		return dir.Sync()
	}
	for attempt := range 2 {
		if _, err := assets.Stage(7, digest, autoinstallTestData); !errors.Is(err, injected) {
			t.Fatalf("attempt %d bypassed failed sync: %v", attempt+1, err)
		}
	}
	assets.syncDirectory = (*os.File).Sync
	if _, err := assets.Stage(7, digest, autoinstallTestData); err != nil {
		t.Fatal(err)
	}
}

func TestAutoinstallConcurrentStagesDoNotInheritUnsyncedSuccess(t *testing.T) {
	for _, losingRename := range []bool{false, true} {
		t.Run(fmt.Sprintf("losing-rename=%v", losingRename), func(t *testing.T) {
			root := t.TempDir()
			winner, err := NewAutoinstallAssets(root)
			if err != nil {
				t.Fatal(err)
			}
			follower, err := NewAutoinstallAssets(root)
			if err != nil {
				t.Fatal(err)
			}
			digest := autoinstallDigest(autoinstallTestData)
			renamed := make(chan struct{})
			releaseWinner := make(chan struct{})
			defer close(releaseWinner)
			winner.syncDirectory = func(dir *os.File) error {
				if dir.Name() == "7" {
					close(renamed)
					<-releaseWinner
				}
				return dir.Sync()
			}
			winnerResult := make(chan error, 1)
			followerReady := make(chan struct{})
			releaseFollower := make(chan struct{})
			defer close(releaseFollower)
			injected := errors.New("injected follower sync failure")
			follower.syncDirectory = func(dir *os.File) error {
				if strings.HasPrefix(dir.Name(), ".stage-") {
					close(followerReady)
					<-releaseFollower
				}
				if dir.Name() == "7" {
					return injected
				}
				return dir.Sync()
			}
			followerResult := make(chan error, 1)
			if losingRename {
				go func() { _, err := follower.Stage(7, digest, autoinstallTestData); followerResult <- err }()
				select {
				case <-followerReady:
				case <-time.After(5 * time.Second):
					t.Fatal("follower did not reach pre-rename sync")
				}
			}
			go func() { _, err := winner.Stage(7, digest, autoinstallTestData); winnerResult <- err }()
			select {
			case <-renamed:
			case <-time.After(5 * time.Second):
				t.Fatal("winner did not pause after rename")
			}
			if losingRename {
				releaseFollower <- struct{}{}
			} else {
				go func() { _, err := follower.Stage(7, digest, autoinstallTestData); followerResult <- err }()
			}
			select {
			case err := <-followerResult:
				if !errors.Is(err, injected) {
					t.Errorf("follower inherited unsynced success: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("follower did not finish")
			}
			releaseWinner <- struct{}{}
			select {
			case err := <-winnerResult:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("winner did not finish")
			}
		})
	}
}
