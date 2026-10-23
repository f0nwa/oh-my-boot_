package control

import (
	"bytes"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The machine list as a CSV file: download it, change it in Excel or a text
// editor, and load it back. Loading shows what will change before anything
// is written.

const (
	maxMachineCSVBytes = 1 << 20
	maxMachineCSVRows  = 5000
	machineImportTTL   = 30 * time.Minute
)

// machineCSVHeader is the column order of the exported file.
var machineCSVHeader = []string{"MAC", "Имя", "Производитель", "Модель", "Серийный номер", "UUID", "Заметка", "Впервые", "Последний раз"}

// machineCSVColumns maps column titles a file may use to the fields that can
// be loaded. Titles are compared in lower case, in Russian or English.
var machineCSVColumns = func() map[string]string {
	columns := map[string]string{
		"mac": "mac", "mac address": "mac", "mac-address": "mac",
		"name":         "name",
		"manufacturer": "manufacturer", "maker": "manufacturer", "vendor": "manufacturer",
		"model": "product", "product": "product",
		"note": "note", "comment": "note",
	}
	// The titles of the exported file, as shown in the interface.
	for title, field := range map[string]string{"MAC-адрес": "mac", "Имя": "name", "Производитель": "manufacturer", "Модель": "product", "Заметка": "note", "Комментарий": "note"} {
		columns[strings.ToLower(title)] = field
	}
	return columns
}()

// machineFieldLimits are the longest values the machine forms accept.
var machineFieldLimits = map[string]int{"name": 80, "manufacturer": 120, "product": 120, "note": 500}

// csvSafe keeps a cell that a spreadsheet would run as a formula as text.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// csvUnsafe undoes csvSafe when a file comes back.
func csvUnsafe(v string) string {
	if len(v) > 1 && v[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(v[1])) {
		return v[1:]
	}
	return v
}

// machinesCSV sends the list, narrowed by the same search as the table, as
// UTF-8 with a BOM and semicolons, which Excel opens with Cyrillic intact.
func (s *Server) machinesCSV(w http.ResponseWriter, r *http.Request) {
	machines, ok := s.refreshedBook(r.Context())
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	list := buildMachineList(machines, r.URL.Query().Get("q"), r.URL.Query().Get("sort"), s.macRules(r.Context()), r.URL.Query().Get("rule"))
	lang := normalizeLanguage(requestInfo(r).User.Language)
	var buf bytes.Buffer
	buf.WriteString("\xef\xbb\xbf")
	out := csv.NewWriter(&buf)
	out.Comma, out.UseCRLF = ';', true
	header := make([]string, len(machineCSVHeader))
	for i, title := range machineCSVHeader {
		header[i] = translate(lang, title)
	}
	_ = out.Write(header)
	stamp := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Local().Format("2006-01-02 15:04")
	}
	for _, row := range list.Rows {
		last := stamp(row.LastSeen)
		if row.Manual {
			last = ""
		}
		_ = out.Write([]string{row.MAC, csvSafe(row.Name), csvSafe(row.Maker()), csvSafe(row.Model()), csvSafe(row.Serial), csvSafe(row.UUID), csvSafe(row.Note), stamp(row.FirstSeen), last})
	}
	out.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="machines-%s.csv"`, time.Now().Format("2006-01-02")))
	_, _ = w.Write(buf.Bytes())
}

// machineImportRow is one machine line of a loaded file. Fields holds only
// the columns the file has; an empty value there changes nothing.
type machineImportRow struct {
	Line   int
	MAC    string
	Fields map[string]string
}

// machineImportError is a line that was skipped, and why.
type machineImportError struct {
	Line        int
	Text, Value string
}

type machineImportFile struct {
	Rows    []machineImportRow
	Errors  []machineImportError
	Ignored []string // columns that are only for reading, such as the serial number
}

// parseMachineCSV reads a machine list. It takes a BOM, semicolons or commas,
// quoted cells with separators and line breaks, and titles in Russian or
// English. Bad lines are reported and skipped; an error means the whole file
// cannot be read.
func parseMachineCSV(data []byte) (machineImportFile, error) {
	var file machineImportFile
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return file, errors.New("Файл пустой")
	}
	if !utf8.Valid(data) {
		return file, errors.New("Файл не в кодировке UTF-8. В Excel сохраните его как «CSV UTF-8 (разделители — запятые)»")
	}
	firstLine, _, _ := bytes.Cut(data, []byte("\n"))
	in := csv.NewReader(bytes.NewReader(data))
	in.Comma = ','
	if bytes.Count(firstLine, []byte(";")) >= bytes.Count(firstLine, []byte(",")) {
		in.Comma = ';'
	}
	in.FieldsPerRecord, in.LazyQuotes = -1, true
	header, err := in.Read()
	if err != nil {
		return file, errors.New("Не удалось прочитать первую строку файла")
	}
	columns := make([]string, len(header))
	hasMAC := false
	for i, title := range header {
		key := strings.ToLower(strings.TrimSpace(title))
		columns[i] = machineCSVColumns[key]
		hasMAC = hasMAC || columns[i] == "mac"
		if columns[i] == "" && strings.TrimSpace(title) != "" {
			file.Ignored = append(file.Ignored, strings.TrimSpace(title))
		}
	}
	if !hasMAC {
		return file, errors.New("В файле нет колонки MAC: первая строка должна содержать названия колонок")
	}
	seen := map[string]int{}
	for {
		line, _ := in.FieldPos(0)
		record, err := in.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return file, fmt.Errorf("Не удалось прочитать строку %d", line)
		}
		line, _ = in.FieldPos(0)
		empty := true
		for _, cell := range record {
			empty = empty && strings.TrimSpace(cell) == ""
		}
		if empty {
			continue
		}
		if len(file.Rows)+len(file.Errors) >= maxMachineCSVRows {
			return file, fmt.Errorf("В файле больше %d строк", maxMachineCSVRows)
		}
		row := machineImportRow{Line: line, Fields: map[string]string{}}
		rawMAC, bad := "", ""
		for i, cell := range record {
			if i >= len(columns) || columns[i] == "" {
				continue
			}
			if columns[i] == "mac" {
				rawMAC = cell
				continue
			}
			value, ok := cleanMachineText(csvUnsafe(cell), machineFieldLimits[columns[i]])
			if !ok {
				bad = cell
			}
			row.Fields[columns[i]] = value
		}
		mac, ok := parseMAC(rawMAC)
		switch {
		case !ok:
			file.Errors = append(file.Errors, machineImportError{Line: line, Text: "неверный MAC-адрес", Value: rawMAC})
		case seen[mac] > 0:
			file.Errors = append(file.Errors, machineImportError{Line: line, Text: fmt.Sprintf("MAC уже был в строке %d", seen[mac]), Value: rawMAC})
		case bad != "":
			file.Errors = append(file.Errors, machineImportError{Line: line, Text: "слишком длинный текст или недопустимые символы", Value: shorten(bad, 40)})
		default:
			row.MAC = mac
			seen[mac] = line
			file.Rows = append(file.Rows, row)
		}
	}
	return file, nil
}

func shorten(v string, n int) string {
	r := []rune(v)
	if len(r) <= n {
		return v
	}
	return string(r[:n]) + "…"
}

// machineImportChange is what loading a file does to one machine: Edit holds
// the values it will have afterwards, Before what it has now.
type machineImportChange struct {
	Line         int
	MAC          string
	Add          bool
	Edit, Before MachineEdit
	Fields       []string // changed fields: name, manufacturer, product, note
	Reported     MachineInfo
}

// FieldChanged tells the preview which cells to mark.
func (c machineImportChange) FieldChanged(field string) bool {
	for _, f := range c.Fields {
		if f == field {
			return true
		}
	}
	return false
}

type machineImportPlan struct {
	Token                string
	Adds, Changes, Same  int
	Items                []machineImportChange
	Errors               []machineImportError
	Ignored              []string
	Shown, HiddenChanges int
}

// planMachineImport compares a file with the list. New MACs are added.
// For known machines a filled cell replaces the name, maker, model or note;
// an empty cell erases nothing. What the machine reported itself (SMBIOS)
// is never changed: a maker or model equal to the reported one means «as
// reported» and clears a person's override.
func planMachineImport(file machineImportFile, book []MachineInfo) machineImportPlan {
	plan := machineImportPlan{Errors: file.Errors, Ignored: file.Ignored}
	known := make(map[string]MachineInfo, len(book))
	for _, m := range book {
		known[m.MAC] = m
	}
	for _, row := range file.Rows {
		m, ok := known[row.MAC]
		if !ok {
			plan.Adds++
			plan.Items = append(plan.Items, machineImportChange{Line: row.Line, MAC: row.MAC, Add: true, Edit: MachineEdit{Name: row.Fields["name"], Manufacturer: row.Fields["manufacturer"], Product: row.Fields["product"], Note: row.Fields["note"]}})
			continue
		}
		before := MachineEdit{Name: m.Name, Note: m.Note, Manufacturer: m.ManufacturerOverride, Product: m.ProductOverride}
		after := before
		if v := row.Fields["name"]; v != "" {
			after.Name = v
		}
		if v := row.Fields["note"]; v != "" {
			after.Note = v
		}
		override := func(v, reported, current string) string {
			switch {
			case v == "":
				return current
			case v == reported:
				return ""
			}
			return v
		}
		after.Manufacturer = override(row.Fields["manufacturer"], m.Manufacturer, before.Manufacturer)
		after.Product = override(row.Fields["product"], m.Product, before.Product)
		change := machineImportChange{Line: row.Line, MAC: row.MAC, Edit: after, Before: before, Reported: m}
		for _, f := range []struct {
			name      string
			old, next string
		}{{"name", before.Name, after.Name}, {"manufacturer", before.Manufacturer, after.Manufacturer}, {"product", before.Product, after.Product}, {"note", before.Note, after.Note}} {
			if f.old != f.next {
				change.Fields = append(change.Fields, f.name)
			}
		}
		if len(change.Fields) == 0 {
			plan.Same++
			continue
		}
		plan.Changes++
		plan.Items = append(plan.Items, change)
	}
	return plan
}

// machineImportStash keeps a loaded file between the preview and «Применить»,
// for the person who loaded it, for half an hour.
type machineImportStash struct {
	mu    sync.Mutex
	items map[string]stashedMachineImport
}

type stashedMachineImport struct {
	user int64
	file machineImportFile
	at   time.Time
}

func (st *machineImportStash) put(user int64, file machineImportFile) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.items == nil {
		st.items = map[string]stashedMachineImport{}
	}
	for k, v := range st.items {
		if time.Since(v.at) > machineImportTTL {
			delete(st.items, k)
		}
	}
	st.items[token] = stashedMachineImport{user: user, file: file, at: time.Now()}
	return token, nil
}

// take returns a stashed file once.
func (st *machineImportStash) take(user int64, token string) (machineImportFile, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	item, ok := st.items[token]
	if !ok || item.user != user || time.Since(item.at) > machineImportTTL {
		return machineImportFile{}, false
	}
	delete(st.items, token)
	return item.file, true
}

// machineImportPreview reads an uploaded file and shows what it would change.
func (s *Server) machineImportPreview(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	upload, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Выберите файл CSV", http.StatusBadRequest)
		return
	}
	defer upload.Close()
	data, err := io.ReadAll(io.LimitReader(upload, maxMachineCSVBytes+1))
	if err != nil || len(data) > maxMachineCSVBytes {
		http.Error(w, "Файл больше 1 МБ", http.StatusRequestEntityTooLarge)
		return
	}
	file, err := parseMachineCSV(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	machines, err := book.Machines(r.Context())
	if err != nil {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	plan := planMachineImport(file, machines)
	if plan.Adds+plan.Changes > 0 {
		if plan.Token, err = s.machineImports.put(requestInfo(r).User.ID, file); err != nil {
			http.Error(w, "Не удалось сохранить файл для применения", http.StatusServiceUnavailable)
			return
		}
	}
	const show = 300
	if len(plan.Items) > show {
		plan.HiddenChanges = len(plan.Items) - show
		plan.Items = plan.Items[:show]
	}
	page := s.machinesData(r)
	page.MachineImport = &plan
	render(w, "machines", page)
}

// machineImportApply writes a previewed file. The plan is made again against
// the list as it is now, so changes made since the preview are not undone
// by stale values; everything is written in one transaction.
func (s *Server) machineImportApply(w http.ResponseWriter, r *http.Request) {
	book, ok := s.Store.(MachineBook)
	if !ok {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	user := requestInfo(r).User.ID
	file, ok := s.machineImports.take(user, r.PostFormValue("token"))
	if !ok {
		http.Error(w, "Проверка файла устарела или уже применена. Загрузите файл ещё раз.", http.StatusGone)
		return
	}
	machines, err := book.Machines(r.Context())
	if err != nil {
		http.Error(w, "Список машин недоступен", http.StatusServiceUnavailable)
		return
	}
	plan := planMachineImport(file, machines)
	if err := book.ApplyMachineImport(r.Context(), plan.Items); err != nil {
		http.Error(w, "Не удалось применить файл: список не изменён", http.StatusServiceUnavailable)
		return
	}
	for _, it := range plan.Items {
		s.bookCache.forget(it.MAC)
	}
	_ = s.Store.Audit(r.Context(), user, "machine.import", "ok", fmt.Sprintf("added=%d changed=%d", plan.Adds, plan.Changes))
	message := fmt.Sprintf("Файл применён: добавлено %d, изменено %d", plan.Adds, plan.Changes)
	http.Redirect(w, r, "/machines?message="+url.QueryEscape(message), http.StatusSeeOther)
}

// machineImportCell is one cell of the preview: the value now and after.
type machineImportCell struct {
	Old, New string
	Changed  bool
}

// Cell shows a field as the table would: a maker or model left empty means
// what the machine reported.
func (c machineImportChange) Cell(field string) machineImportCell {
	pick := func(e MachineEdit) string {
		switch field {
		case "name":
			return e.Name
		case "note":
			return e.Note
		case "manufacturer":
			if e.Manufacturer == "" {
				return c.Reported.Manufacturer
			}
			return e.Manufacturer
		case "product":
			if e.Product == "" {
				return c.Reported.Product
			}
			return e.Product
		}
		return ""
	}
	cell := machineImportCell{New: pick(c.Edit)}
	if !c.Add {
		cell.Old = pick(c.Before)
		cell.Changed = c.FieldChanged(field)
	}
	return cell
}

// ExportLink is the CSV address for the current search.
func (l machineList) ExportLink() string {
	v := url.Values{}
	if l.Query != "" {
		v.Set("q", l.Query)
	}
	if l.RuleFilter != nil {
		v.Set("rule", l.RuleFilter.Prefix)
	}
	if len(v) == 0 {
		return "/machines.csv"
	}
	return "/machines.csv?" + v.Encode()
}
