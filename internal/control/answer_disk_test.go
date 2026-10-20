package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAnswerWipesDisk(t *testing.T) {
	keyOnly, err := os.ReadFile("../../examples/windows-answer-key-no-disk.xml")
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		xml  string
		want bool
	}{
		"will wipe":        {`<unattend><settings><component><DiskConfiguration><Disk><WillWipeDisk>true</WillWipeDisk></Disk></DiskConfiguration></component></settings></unattend>`, true},
		"will wipe spaced": {`<unattend><DiskConfiguration><Disk><WillWipeDisk> True </WillWipeDisk></Disk></DiskConfiguration></unattend>`, true},
		"no wipe":          {`<unattend><DiskConfiguration><Disk><WillWipeDisk>false</WillWipeDisk></Disk></DiskConfiguration></unattend>`, false},
		"partitions":       {`<unattend><DiskConfiguration><Disk><CreatePartitions><CreatePartition><Order>1</Order></CreatePartition></CreatePartitions></Disk></DiskConfiguration></unattend>`, true},
		"namespaced":       {`<u:unattend xmlns:u="urn:schemas-microsoft-com:unattend"><u:DiskConfiguration><u:Disk><u:WillWipeDisk>true</u:WillWipeDisk></u:Disk></u:DiskConfiguration></u:unattend>`, true},
		"key only file":    {string(keyOnly), false},
		"diskpart script":  {`<unattend><settings pass="windowsPE"><component><RunSynchronous><RunSynchronousCommand><Order>1</Order><Path>cmd.exe /c "&gt;&gt;"X:\diskpart.txt" (echo:SELECT DISK=0&amp;echo:CLEAN)"</Path></RunSynchronousCommand><RunSynchronousCommand><Order>2</Order><Path>cmd.exe /c "diskpart.exe /s "X:\diskpart.txt""</Path></RunSynchronousCommand></RunSynchronous></component></settings></unattend>`, true},
		"install to":       {`<unattend><ImageInstall><OSImage><InstallTo><DiskID>0</DiskID><PartitionID>3</PartitionID></InstallTo></OSImage></ImageInstall></unattend>`, true},
		"harmless command": {`<unattend><RunSynchronous><RunSynchronousCommand><Path>reg.exe add HKLM\Software\X</Path></RunSynchronousCommand></RunSynchronous></unattend>`, false},
	} {
		if got, known := AnswerWipesDisk([]byte(tc.xml)); !known || got != tc.want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}

type inspectStore struct {
	answerPageStore
	flags map[int64]bool
}

func (s *inspectStore) WindowsAnswer(_ context.Context, id int64) (WindowsAnswerVariant, error) {
	for _, a := range s.answers {
		if a.ID == id {
			return a, nil
		}
	}
	return WindowsAnswerVariant{}, os.ErrNotExist
}

func (s *inspectStore) SetWindowsAnswerWipesDisk(_ context.Context, id int64, wipes bool) error {
	s.flags[id] = wipes
	return nil
}

func inspectRequest(t *testing.T, path string, body []byte) *http.Request {
	t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	_ = w.WriteField("csrf", CSRFToken("test-token"))
	f, _ := w.CreateFormFile("file", "autounattend.xml")
	_, _ = f.Write(body)
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, path, &data)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	return req
}

func TestAnswerInspectRejectsDifferentFile(t *testing.T) {
	store := &inspectStore{answerPageStore: answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, answers: []WindowsAnswerVariant{{ID: 3, ProfileID: 1, SHA256: strings.Repeat("a", 64), State: "ready"}}}, flags: map[int64]bool{}}
	s := &Server{Store: store, Images: &fakeImagesAPI{}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, inspectRequest(t, "/answers/3/inspect", []byte(`<unattend/>`)))
	if res.Code != http.StatusConflict || !strings.Contains(res.Body.String(), "контрольная сумма не совпадает") || !strings.Contains(res.Body.String(), `class="page-head"`) || len(store.flags) != 0 {
		t.Fatalf("status=%d flags=%v body=%s", res.Code, store.flags, res.Body.String())
	}
}

func TestAnswerInspectStoresFlagForSameFile(t *testing.T) {
	xml := []byte(`<unattend><DiskConfiguration><Disk><WillWipeDisk>true</WillWipeDisk></Disk></DiskConfiguration></unattend>`)
	store := &inspectStore{answerPageStore: answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, answers: []WindowsAnswerVariant{{ID: 3, ProfileID: 1, SHA256: sha256Hex(xml), State: "ready"}}}, flags: map[int64]bool{}}
	s := &Server{Store: store, Images: &fakeImagesAPI{}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, inspectRequest(t, "/answers/3/inspect", xml))
	if res.Code != http.StatusSeeOther || !store.flags[3] {
		t.Fatalf("status=%d flags=%v body=%s", res.Code, store.flags, res.Body.String())
	}
}

func TestAnswerUploadStoresWipeFlag(t *testing.T) {
	xml := []byte(`<unattend xmlns="urn:schemas-microsoft-com:unattend"><settings pass="windowsPE"><component><DiskConfiguration><Disk><WillWipeDisk>true</WillWipeDisk></Disk></DiskConfiguration></component></settings></unattend>`)
	store := &answerPageStore{authOnlyStore: authOnlyStore{user: User{ID: 1, Role: "operator", Enabled: true}}, profile: WindowsProfile{ID: 17, State: "ready"}}
	s := &Server{Store: store, WindowsAnswers: &answerAgent{}, Images: &fakeImagesAPI{}}
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, answerUploadRequest(t, xml, CSRFToken("test-token")))
	if res.Code != http.StatusSeeOther || len(store.answers) != 1 || store.answers[0].WipesDisk == nil || !*store.answers[0].WipesDisk {
		t.Fatalf("status=%d answers=%+v", res.Code, store.answers)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
