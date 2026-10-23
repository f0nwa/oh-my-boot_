package control

import (
	"bytes"
	"context"
	"encoding/csv"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMachinesCSVExport(t *testing.T) {
	store := machinesFixture() // observer may download
	m := store.machines["bc:24:11:52:02:37"]
	m.Note = "=HYPERLINK(\"http://x\");\"кавычки\""
	m.ProductOverride = "Lab VM"
	store.machines["bc:24:11:52:02:37"] = m
	s := &Server{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/machines.csv", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Type"), "text/csv") || !regexp.MustCompile(`attachment; filename="machines-\d{4}-\d{2}-\d{2}\.csv"`).MatchString(res.Header().Get("Content-Disposition")) {
		t.Fatalf("code=%d headers=%v", res.Code, res.Header())
	}
	body := res.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
		t.Fatal("no BOM")
	}
	r := csv.NewReader(bytes.NewReader(body[3:]))
	r.Comma = ';'
	records, err := r.ReadAll()
	if err != nil || len(records) != 4 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	if strings.Join(records[0], "|") != "MAC|Имя|Производитель|Модель|Серийный номер|UUID|Заметка|Впервые|Последний раз" {
		t.Fatalf("header=%v", records[0])
	}
	var vm []string
	for _, rec := range records[1:] {
		if rec[0] == "bc:24:11:52:02:37" {
			vm = rec
		}
	}
	if vm == nil || vm[1] != "test-vm-17" || vm[2] != "QEMU" || vm[3] != "Lab VM" || vm[4] != "SN-VM17" || vm[7] != time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC).Local().Format("2006-01-02 15:04") {
		t.Fatalf("row=%q", vm)
	}
	// A cell that Excel would run as a formula is kept as text.
	if vm[6] != "'=HYPERLINK(\"http://x\");\"кавычки\"" {
		t.Fatalf("note=%q", vm[6])
	}
	if !bytes.Contains(body, []byte("\r\n")) {
		t.Fatal("not CRLF")
	}
	// The search narrows the file like the table.
	req = httptest.NewRequest(http.MethodGet, "/machines.csv?q=elitebook", nil)
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res = httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if n := strings.Count(res.Body.String(), "\r\n"); n != 2 {
		t.Fatalf("filtered lines=%d", n)
	}
}

func TestParseMachineCSV(t *testing.T) {
	file := "\xef\xbb\xbf" + "mac;Имя;Модель;Серийный номер;Заметка\r\n" +
		"BC-24-11-AA-BB-01;\"Бухгалтерия; 3\";ThinkPad T14;IGNORED;\"многострочная\nзаметка\"\r\n" +
		"\r\n" +
		"not-a-mac;x;;;\r\n" +
		"bc2411aabb01;dup;;;\r\n" +
		"bc:24:11:aa:bb:02;" + strings.Repeat("x", 81) + ";;;\r\n" +
		"bc:24:11:aa:bb:03;'=1+1;;;\r\n"
	got, err := parseMachineCSV([]byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[0].MAC != "bc:24:11:aa:bb:01" || got.Rows[0].Fields["name"] != "Бухгалтерия; 3" || got.Rows[0].Fields["product"] != "ThinkPad T14" || got.Rows[0].Fields["note"] != "многострочная\nзаметка" || got.Rows[0].Line != 2 {
		t.Fatalf("rows=%+v", got.Rows)
	}
	if _, ok := got.Rows[0].Fields["manufacturer"]; ok {
		t.Fatal("a missing column became a field")
	}
	if got.Rows[1].Fields["name"] != "=1+1" {
		t.Fatalf("formula guard not undone: %q", got.Rows[1].Fields["name"])
	}
	var lines []string
	for _, e := range got.Errors {
		lines = append(lines, strconv.Itoa(e.Line)+":"+e.Text)
	}
	if strings.Join(lines, "|") != "5:неверный MAC-адрес|6:MAC уже был в строке 2|7:слишком длинный текст или недопустимые символы" {
		t.Fatalf("errors=%v", lines)
	}
	if strings.Join(got.Ignored, ",") != "Серийный номер" {
		t.Fatalf("ignored=%v", got.Ignored)
	}
	// Comma-separated, English headers.
	got, err = parseMachineCSV([]byte("MAC,Name,Maker,Model,Note\nbc:24:11:aa:bb:04,lab,Dell Inc.,OptiPlex,\n"))
	if err != nil || len(got.Rows) != 1 || got.Rows[0].Fields["manufacturer"] != "Dell Inc." || got.Rows[0].Fields["note"] != "" {
		t.Fatalf("english: %+v %v", got, err)
	}
	for name, bad := range map[string]string{
		"no MAC column": "Имя;Модель\nx;y\n",
		"not UTF-8":     "MAC;Имя\nbc:24:11:aa:bb:01;\xc8\xec\xff\n",
		"empty":         "",
	} {
		if _, err := parseMachineCSV([]byte(bad)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestPlanMachineImportMergeRules(t *testing.T) {
	book := []MachineInfo{
		{MAC: "3c:52:82:01:02:03", Manufacturer: "HP", Product: "EliteBook 840 G8", Name: "old", Note: "keep"},
		{MAC: "00:1b:21:aa:bb:cc", Name: "same"},
		{MAC: "bc:24:11:52:02:37", Manufacturer: "QEMU", Product: "Standard PC", ProductOverride: "Lab VM"},
	}
	file := machineImportFile{Rows: []machineImportRow{
		// Empty note does not erase; model equal to what the machine reported is not an override.
		{Line: 2, MAC: "3c:52:82:01:02:03", Fields: map[string]string{"name": "hr-laptop", "note": "", "product": "EliteBook 840 G8"}},
		{Line: 3, MAC: "00:1b:21:aa:bb:cc", Fields: map[string]string{"name": "same"}},
		// Writing the reported model again drops the person's override.
		{Line: 4, MAC: "bc:24:11:52:02:37", Fields: map[string]string{"product": "Standard PC"}},
		{Line: 5, MAC: "bc:24:11:aa:bb:01", Fields: map[string]string{"name": "new", "manufacturer": "Lenovo"}},
	}}
	plan := planMachineImport(file, book)
	if plan.Adds != 1 || plan.Changes != 2 || plan.Same != 1 || len(plan.Items) != 3 {
		t.Fatalf("plan=%+v", plan)
	}
	byMAC := map[string]machineImportChange{}
	for _, it := range plan.Items {
		byMAC[it.MAC] = it
	}
	if c := byMAC["3c:52:82:01:02:03"]; c.Add || c.Edit.Name != "hr-laptop" || c.Edit.Note != "keep" || c.Edit.Product != "" || strings.Join(c.Fields, ",") != "name" {
		t.Fatalf("hp=%+v", c)
	}
	if c := byMAC["bc:24:11:52:02:37"]; c.Edit.Product != "" || strings.Join(c.Fields, ",") != "product" {
		t.Fatalf("vm=%+v", c)
	}
	if c := byMAC["bc:24:11:aa:bb:01"]; !c.Add || c.Edit.Name != "new" || c.Edit.Manufacturer != "Lenovo" {
		t.Fatalf("new=%+v", c)
	}
}

func csvUpload(t *testing.T, s *Server, content string) *httptest.ResponseRecorder {
	t.Helper()
	var data bytes.Buffer
	w := multipart.NewWriter(&data)
	_ = w.WriteField("csrf", CSRFToken("test-token"))
	f, _ := w.CreateFormFile("file", "machines.csv")
	_, _ = f.Write([]byte(content))
	_ = w.Close()
	req := httptest.NewRequest(http.MethodPost, "/machines/import", &data)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	return res
}

var importToken = regexp.MustCompile(`name="token" value="([0-9a-f]{32})"`)

func TestMachineImportPreviewAndApply(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}
	res := csvUpload(t, s, "MAC;Имя;Заметка\n3c:52:82:01:02:03;hr-laptop;\nbc:24:11:aa:bb:01;new-one;ждёт\nbad;x;\n")
	page := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", res.Code, page)
	}
	for _, want := range []string{"Проверка файла", "Добавится: 1", "Изменится: 1", "Без изменений: 0", "Ошибок: 1", "неверный MAC-адрес", "hr-laptop", "new-one"} {
		if !strings.Contains(page, want) {
			t.Fatalf("preview lacks %q: %s", want, page)
		}
	}
	if store.machines["3c:52:82:01:02:03"].Name != "" || len(store.machines) != 3 {
		t.Fatal("preview changed the book")
	}
	m := importToken.FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("no apply form: %s", page)
	}
	// Another user cannot apply it.
	other := &Server{Store: &bookStore{authOnlyStore: authOnlyStore{user: User{ID: 2, Role: "operator", Enabled: true}}, machines: store.machines}, machineImports: s.machineImports}
	if res := menuPost(t, other, "/machines/import-apply", url.Values{"token": {m[1]}}); res.Code != http.StatusGone {
		t.Fatalf("other user: %d", res.Code)
	}
	res = menuPost(t, s, "/machines/import-apply", url.Values{"token": {m[1]}})
	if res.Code != http.StatusSeeOther || !strings.HasPrefix(res.Header().Get("Location"), "/machines?message=") {
		t.Fatalf("apply: %d %s", res.Code, res.Body)
	}
	if store.machines["3c:52:82:01:02:03"].Name != "hr-laptop" || store.machines["bc:24:11:aa:bb:01"].Name != "new-one" || !store.machines["bc:24:11:aa:bb:01"].Manual {
		t.Fatalf("book=%+v", store.machines)
	}
	if last := store.audits[len(store.audits)-1]; last != "machine.import ok added=1 changed=1" {
		t.Fatalf("audit=%v", store.audits)
	}
	// A preview is applied once.
	if res := menuPost(t, s, "/machines/import-apply", url.Values{"token": {m[1]}}); res.Code != http.StatusGone {
		t.Fatalf("second apply: %d", res.Code)
	}
}

func TestMachineImportRejectsBadFilesAndObservers(t *testing.T) {
	store := operatorBook()
	s := &Server{Store: store}
	res := csvUpload(t, s, "Имя\nx\n")
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "нет колонки MAC") {
		t.Fatalf("no MAC column: %d %s", res.Code, res.Body)
	}
	res = csvUpload(t, s, "MAC;Имя\nbc:24:11:aa:bb:01;\xc8\xec\xff\n")
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "CSV UTF-8") {
		t.Fatalf("not UTF-8: %d", res.Code)
	}
	res = csvUpload(t, s, "MAC\n"+strings.Repeat("bc:24:11:aa:bb:01\n", 70000))
	if res.Code != http.StatusRequestEntityTooLarge && res.Code != http.StatusBadRequest {
		t.Fatalf("big file: %d", res.Code)
	}
	store.user.Role = "observer"
	if res := csvUpload(t, s, "MAC\nbc:24:11:aa:bb:01\n"); res.Code != http.StatusForbidden {
		t.Fatalf("observer import: %d", res.Code)
	}
	if page := libraryGet(t, s, "/machines"); strings.Contains(page, `action="/machines/import"`) || !strings.Contains(page, `href="/machines.csv"`) {
		t.Fatal("observer: import form shown or no download")
	}
}

// A downloaded file loads back without changes, in either language.
func TestMachinesCSVRoundTrip(t *testing.T) {
	for _, lang := range []string{langRU, langEN} {
		store := operatorBook()
		store.user.Language = lang
		m := store.machines["bc:24:11:52:02:37"]
		m.Note, m.ManufacturerOverride = "-минус в начале", "Proxmox"
		store.machines["bc:24:11:52:02:37"] = m
		s := &Server{Store: store}
		req := httptest.NewRequest(http.MethodGet, "/machines.csv", nil)
		req.AddCookie(&http.Cookie{Name: "pxe_session", Value: "test-token"})
		res := httptest.NewRecorder()
		s.Handler().ServeHTTP(res, req)
		file, err := parseMachineCSV(res.Body.Bytes())
		if err != nil || len(file.Rows) != 3 || len(file.Errors) != 0 {
			t.Fatalf("%s: %+v %v", lang, file, err)
		}
		book, _ := store.Machines(context.Background())
		if plan := planMachineImport(file, book); plan.Adds != 0 || plan.Changes != 0 || plan.Same != 3 {
			t.Fatalf("%s: round trip changes %+v", lang, plan)
		}
	}
}
