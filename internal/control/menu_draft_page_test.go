package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pxe-platform/internal/images"
)

// fakeBoot serves the Boot socket API and records published scripts.
func fakeBoot(t *testing.T) (*BootClient, *[]string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "boot")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "b.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	var published []string
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/menu":
			_, _ = w.Write([]byte("#!ipxe\n"))
		case "/publish":
			body, _ := io.ReadAll(r.Body)
			published = append(published, string(body))
			sum := sha256.Sum256(body)
			_ = json.NewEncoder(w).Encode(map[string]string{"sha256": hex.EncodeToString(sum[:])})
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return NewBootClient(path), &published
}

func draftServer(t *testing.T, store *draftStore) (*Server, *[]string) {
	boot, published := fakeBoot(t)
	p := store.profiles[0]
	api := &fakeImagesAPI{catalog: images.Catalog{Storages: []images.Storage{{ID: p.StorageID, Kind: "smb", Root: p.StorageRoot, State: "ready"}}, Images: []images.Image{{StorageID: p.StorageID, Path: p.Path, Size: p.Size, Modified: p.Modified, Present: true}}}}
	return &Server{Store: store, Boot: boot, Images: api, WindowsISO: &recordingWindowsISOAPI{}, WindowsAnswers: &answerAgent{}, BootBaseURL: testBootURL}, published
}

func menuPost(t *testing.T, s *Server, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, accountRequest(http.MethodPost, path, form))
	return res
}

func TestPublishBuildsFromDraftAndKeepsIt(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/publish", url.Values{})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/menu?message=") || len(*published) != 1 || !strings.Contains((*published)[0], "item winiso_17 Windows 11") || len(store.saved) == 0 {
		t.Fatalf("status=%d location=%q published=%d saved=%d body=%s", res.Code, res.Header().Get("Location"), len(*published), len(store.saved), res.Body.String())
	}
}

func TestMoveDownChangesOrderOnly(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17"}, {Key: "winanswer_31"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/items/winiso_17/move", url.Values{"dir": {"down"}})
	if res.Code != http.StatusSeeOther || store.draft.Items[0].Key != "winanswer_31" || store.draft.Items[1].Key != "winiso_17" || len(*published) != 0 {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestRemoveItemFromDraft(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winiso_17"}, {Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	res := menuPost(t, s, "/menu/items/winiso_17/remove", url.Values{})
	if res.Code != http.StatusSeeOther || len(store.draft.Items) != 1 || store.draft.Items[0].Key != "winanswer_31" {
		t.Fatalf("status=%d draft=%+v", res.Code, store.draft)
	}
}

func TestRollbackResetsDraft(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	old := store.versions[0]
	old.ID, old.Active = 31, false
	store.versions[0].Script = "#!ipxe\n"
	store.versions = append(store.versions, old)
	store.draft = &MenuDraft{Title: "Other", Label: "Diagnostics", Message: "Ready"}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/rollback", url.Values{"id": {"31"}})
	if res.Code != http.StatusSeeOther || len(*published) != 1 || store.draft.Title != "PXE Platform" || len(store.draft.Items) != 1 || store.draft.Items[0].Key != "winiso_17" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestRollbackOfUnrestorableVersionKeepsDraft(t *testing.T) {
	store := newDraftStore(t, "operator", []MenuDraftItem{{Key: "winanswer_31", Title: "Lab"}})
	old := store.versions[0]
	old.ID, old.Active = 31, false
	store.versions = append(store.versions, old)
	store.answers = nil
	store.draft = &MenuDraft{Title: "Kept", Label: "Diagnostics", Message: "Ready"}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/rollback", url.Values{"id": {"31"}})
	if res.Code < 400 || !strings.Contains(res.Body.String(), `role="alert"`) || len(*published) != 0 || store.draft.Title != "Kept" {
		t.Fatalf("status=%d draft=%+v body=%s", res.Code, store.draft, res.Body.String())
	}
}

func TestPublishWithMissingObjectShowsError(t *testing.T) {
	store := newDraftStore(t, "operator", nil)
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_99", Title: "Gone"}}}
	s, published := draftServer(t, store)
	res := menuPost(t, s, "/menu/publish", url.Values{})
	body := res.Body.String()
	if res.Code < 400 || !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "winanswer_99") || !strings.Contains(body, "Недоступно") || len(*published) != 0 {
		t.Fatalf("status=%d body=%s", res.Code, body)
	}
}

func TestObserverSeesNoMenuButtons(t *testing.T) {
	store := newDraftStore(t, "observer", []MenuDraftItem{{Key: "winiso_17", Title: "Windows 11"}})
	store.draft = &MenuDraft{Title: "PXE Platform", Label: "Diagnostics", Message: "Ready", Items: []MenuDraftItem{{Key: "winanswer_31"}}}
	s, _ := draftServer(t, store)
	body := libraryGet(t, s, "/menu")
	if strings.Contains(body, `action="/menu/`) || !strings.Contains(body, "Lab") {
		t.Fatalf("observer page: %s", body)
	}
}

var _ = context.Background
