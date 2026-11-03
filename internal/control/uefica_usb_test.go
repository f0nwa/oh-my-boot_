package control

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omb/internal/images"
	"omb/internal/ueficausb"
)

type ueficaStore struct {
	authOnlyStore
	profiles []WindowsProfile
	audits   []string
}

func (s *ueficaStore) WindowsProfiles(context.Context) ([]WindowsProfile, error) {
	return s.profiles, nil
}

// Audit drops the row on a cancelled context, as pgx does.
func (s *ueficaStore) Audit(ctx context.Context, _ int64, action, result, detail string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.audits = append(s.audits, action+"|"+result+"|"+detail)
	return nil
}

type fakeUEFICAUSB struct {
	cancel   context.CancelFunc // set: the download is cancelled on the first read
	archives []ueficausb.Archive
	builds   []string
	buildErr error
	data     string
	pause    time.Duration
}

func (f *fakeUEFICAUSB) BuildUEFICAUSB(_ context.Context, id int64, loader string) (ueficausb.Result, error) {
	f.builds = append(f.builds, loader)
	if f.buildErr != nil {
		return ueficausb.Result{}, f.buildErr
	}
	return ueficausb.Result{Key: "17-" + loader + "-0123456789abcdef", Size: int64(len(f.data))}, nil
}

// slowReader returns its data in two halves with a pause between them.
type slowReader struct {
	parts [][]byte
	pause time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	if len(s.parts) == 0 {
		return 0, io.EOF
	}
	if len(s.parts) == 1 && s.pause > 0 {
		time.Sleep(s.pause)
	}
	n := copy(p, s.parts[0])
	s.parts[0] = s.parts[0][n:]
	if len(s.parts[0]) == 0 {
		s.parts = s.parts[1:]
	}
	return n, nil
}

func (f *fakeUEFICAUSB) UEFICAUSBArchives(context.Context) ([]ueficausb.Archive, error) {
	return f.archives, nil
}

func (f *fakeUEFICAUSB) OpenUEFICAUSB(context.Context, string) (io.ReadCloser, int64, error) {
	if f.cancel != nil {
		return io.NopCloser(&cancelReader{cancel: f.cancel}), int64(len(f.data)), nil
	}
	half := len(f.data) / 2
	return io.NopCloser(&slowReader{parts: [][]byte{[]byte(f.data[:half]), []byte(f.data[half:])}, pause: f.pause}), int64(len(f.data)), nil
}

func ueficaServer(role string) (*Server, *ueficaStore, *fakeUEFICAUSB) {
	store := &ueficaStore{
		authOnlyStore: authOnlyStore{user: User{ID: 1, Role: role, Enabled: true}},
		profiles: []WindowsProfile{
			{ID: 17, WindowsProfileSource: WindowsProfileSource{Title: "Win 11", Path: "w11.iso"}, State: "ready"},
			{ID: 18, WindowsProfileSource: WindowsProfileSource{Title: "Broken", Path: "bad.iso"}, State: "error"},
		},
	}
	usb := &fakeUEFICAUSB{data: "PK-zip-archive-bytes"}
	api := &fakeImagesAPI{catalog: images.Catalog{}}
	return &Server{Store: store, Images: api, UEFICAUSB: usb}, store, usb
}

func ueficaPost(s *Server, id, loader string, csrf bool) *httptest.ResponseRecorder {
	form := url.Values{"profile_id": {id}, "loader": {loader}}
	if csrf {
		form.Set("csrf", CSRFToken("test-token"))
	}
	req := httptest.NewRequest(http.MethodPost, "/usb/uefica", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

func TestUSBPageForEveryRole(t *testing.T) {
	for _, role := range []string{"observer", "operator", "admin"} {
		s, _, _ := ueficaServer(role)
		body := libraryGet(t, s, "/usb")
		for _, want := range []string{`href="/usb"`, "USB-носители", `action="/usb/uefica"`, `name="profile_id" value="17" checked`, `name="loader" value="2011" checked`, "omb-uefica-17-2011.zip", "Скачать архив"} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s: /usb lacks %q", role, want)
			}
		}
		if strings.Contains(body, `value="18"`) {
			t.Fatalf("%s: a profile in error is offered", role)
		}
	}
}

func TestUSBPageSelectionAndCache(t *testing.T) {
	s, _, usb := ueficaServer("admin")
	s.Store.(*ueficaStore).profiles = append(s.Store.(*ueficaStore).profiles, WindowsProfile{ID: 19, WindowsProfileSource: WindowsProfileSource{Title: "Win 10", Path: "w10.iso"}, State: "ready"})
	usb.archives = []ueficausb.Archive{{ProfileID: 19, Loader: "2023", Size: 613436355}}
	body := libraryGet(t, s, "/usb?profile_id=19&loader=2023")
	for _, want := range []string{`name="profile_id" value="19" checked`, `name="loader" value="2023" checked`, "omb-uefica-19-2023.zip", "в кэше", "585,0 МБ"} {
		if !strings.Contains(body, want) {
			t.Fatalf("/usb?profile_id=19 lacks %q", want)
		}
	}
	if strings.Contains(body, `value="17" checked`) {
		t.Fatal("profile 17 still selected")
	}
}

func TestImagesPageLinksToUSB(t *testing.T) {
	s, store, _ := ueficaServer("observer")
 store.profiles[0].InstallFile="install.wim"
 s.Images.(*fakeImagesAPI).catalog.Images=[]images.Image{{Path:"w11.iso",Present:true},{Path:"bad.iso",Present:true}}
 body := libraryGet(t, s, "/images")
	if !strings.Contains(body, `href="/usb?profile_id=17"`) || strings.Contains(body, `href="/usb?profile_id=18"`) || strings.Contains(body, "uefica-usb") {
		t.Fatal("ISO page must link ready profiles to /usb and have no stick form")
	}
}

func TestUEFICAUSBDownload(t *testing.T) {
	s, store, usb := ueficaServer("observer")
	res := ueficaPost(s, "17", "2023", true)
	if res.Code != http.StatusOK || res.Body.String() != usb.data {
		t.Fatalf("status %d body %q", res.Code, res.Body)
	}
	if got := res.Header().Get("Content-Disposition"); got != `attachment; filename="omb-uefica-17-2023.zip"` {
		t.Fatalf("Content-Disposition %q", got)
	}
	if res.Header().Get("Content-Type") != "application/zip" || res.Header().Get("Content-Length") != "20" {
		t.Fatalf("headers %v", res.Header())
	}
	if strings.Join(usb.builds, ",") != "2023" || strings.Join(store.audits, ",") != "uefica-usb.download|ok|17-2023" {
		t.Fatalf("builds %v audits %v", usb.builds, store.audits)
	}
}

func TestUEFICAUSBDownloadRejects(t *testing.T) {
	for _, c := range []struct {
		id, loader string
		csrf       bool
		code       int
		text       string
	}{
		{"17", "x", true, http.StatusBadRequest, "Неизвестный загрузчик"},
		{"18", "2011", true, http.StatusNotFound, "Профиль не подготовлен"},
		{"99", "2011", true, http.StatusNotFound, "Профиль не подготовлен"},
		{"17", "2011", false, http.StatusForbidden, ""},
	} {
		s, _, usb := ueficaServer("admin")
		res := ueficaPost(s, c.id, c.loader, c.csrf)
		if res.Code != c.code || !strings.Contains(res.Body.String(), c.text) || len(usb.builds) != 0 {
			t.Errorf("%+v: status %d builds %v body %.200s", c, res.Code, usb.builds, res.Body)
		}
	}
}

func TestUEFICAUSBBuildErrorAudited(t *testing.T) {
	s, store, usb := ueficaServer("operator")
	usb.buildErr = errors.New("local API /uefica-usb/build: 500 USB stick build failed")
	res := ueficaPost(s, "17", "2011", true)
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "Не удалось собрать флешку") || !strings.Contains(res.Body.String(), "<html") {
		t.Fatalf("status %d body %.300s", res.Code, res.Body)
	}
	if strings.Join(store.audits, ",") != "uefica-usb.download|error|17-2011" {
		t.Fatalf("audits %v", store.audits)
	}
}

func TestUEFICAUSBDownloadExtendsWriteDeadline(t *testing.T) {
	s, _, usb := ueficaServer("admin")
	usb.data = strings.Repeat("z", 4<<20)
	usb.pause = 300 * time.Millisecond
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()
	form := url.Values{"profile_id": {"17"}, "loader": {"2011"}, "csrf": {CSRFToken("test-token")}}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/usb/uefica", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil || len(data) != len(usb.data) {
		t.Fatalf("got %d of %d bytes: %v", len(data), len(usb.data), err)
	}
}

// cancelReader imitates a browser that cancels the download: the request
// context ends and the stream from the Agent breaks.
type cancelReader struct{ cancel context.CancelFunc }

func (c *cancelReader) Read([]byte) (int, error) {
	c.cancel()
	return 0, context.Canceled
}

func TestUEFICAUSBCancelledDownloadIsAudited(t *testing.T) {
	s, store, usb := ueficaServer("operator")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	usb.cancel = cancel
	form := url.Values{"profile_id": {"17"}, "loader": {"2011"}, "csrf": {CSRFToken("test-token")}}
	req := httptest.NewRequest(http.MethodPost, "/usb/uefica", strings.NewReader(form.Encode())).WithContext(ctx)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "omb_session", Value: "test-token"})
	s.Handler().ServeHTTP(httptest.NewRecorder(), req)
	if strings.Join(store.audits, ",") != "uefica-usb.download|error|17-2011" {
		t.Fatalf("audits %v", store.audits)
	}
}
