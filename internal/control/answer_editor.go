package control

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"omb/internal/agent"
	"omb/internal/unattend"
)

// The answer file editor: «Библиотека → Файлы ответов → Редактор». The form
// is unattend.FormValues of the settings; the page shows one section at a
// time, what the file will do and the XML. Without JavaScript the section
// links, presets and «ещё строка» are submit buttons; with it the page
// switches sections in place and asks /answers/editor (do=preview) for the
// summary and XML as the form changes.

//go:embed answer_editor.js
var answerEditorJS []byte

func answerEditorScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(answerEditorJS)
}

// WindowsAnswerReader reads a stored answer file back from the Agent.
type WindowsAnswerReader interface {
	WindowsAnswerXML(context.Context, agent.WindowsAnswerCheck) ([]byte, error)
}

// editorSection is one block of the form. Prefixes are the form fields it
// holds, for the «изменено» marks.
type editorSection struct {
	ID       string
	Title    string
	Prefixes []string
	// Mark is filled per request: "", "changed" or a count; Questions is
	// the number of warnings in the section.
	Mark      string
	Questions int
}

var editorSections = []editorSection{
	{ID: "language", Title: "Язык и регион", Prefixes: []string{"LanguageSettings"}},
	{ID: "disk", Title: "Диск и установка", Prefixes: []string{"PESettings.$type", "PESettings.GeneratePESettings", "PESettings.ScriptPESetttings", "PESettings.DefaultPESettings.BypassRequirementsCheck"}},
	{ID: "edition", Title: "Редакция и ключ", Prefixes: []string{"PESettings.DefaultPESettings.EditionSettings", "ActivationKey"}},
	{ID: "name", Title: "Имя и время", Prefixes: []string{"ComputerNameSettings", "TimeZoneSettings"}},
	{ID: "accounts", Title: "Учётные записи", Prefixes: []string{"AccountSettings", "LockoutSettings", "PasswordExpirationSettings"}},
	{ID: "explorer", Title: "Проводник и «Пуск»", Prefixes: []string{"HideFiles", "ShowFileExtensions", "LaunchToThisPC", "ClassicContextMenu", "LeftTaskbar", "HideTaskViewButton", "ShowAllTrayIcons", "ShowEndTask", "TaskbarSearch", "DisableBingResults", "HideInfoTip", "TaskbarIcons", "StartPinsSettings", "StartTilesSettings", "DesktopIcons", "StartFolderSettings"}},
	{ID: "look", Title: "Оформление", Prefixes: []string{"ColorSettings", "WallpaperSettings", "LockScreenSettings", "Effects", "StickyKeysSettings", "LockKeySettings", "TurnOffSystemSounds", "DisablePointerPrecision"}},
	{ID: "network", Title: "Сеть и ВМ", Prefixes: []string{"WifiSettings", "BypassNetworkCheck", "ExpressSettings", "EnableRemoteDesktop", "VBoxGuestAdditions", "VMwareTools", "VirtIoGuestTools", "ParallelsTools", "ProcessorArchitectures"}},
	{ID: "apps", Title: "Удаление приложений", Prefixes: []string{"Bloatwares"}},
	{ID: "optimize", Title: "Оптимизации и защита", Prefixes: []string{"EnableLongPaths", "HardenSystemDriveAcl", "DeleteJunctions", "AllowPowerShellScripts", "DisableLastAccess", "PreventAutomaticReboot", "DisableSac", "DisableUac", "DisableSmartScreen", "DisableSystemRestore", "DisableFastStartup", "DisableAppSuggestions", "DisableWidgets", "PreventDeviceEncryption", "HideEdgeFre", "DisableEdgeStartupBoost", "MakeEdgeUninstallable", "DeleteEdgeDesktopIcon", "DisableWindowsUpdate", "DeleteWindowsOld", "UseConfigurationSet", "HidePowerShellWindows", "KeepSensitiveFiles", "UseNarrator", "DisableCoreIsolation", "DisableAutomaticRestartSignOn", "DisableWpbt", "PreventDeviceApps", "ProcessAuditSettings", "AppLockerSettings"}},
	{ID: "scripts", Title: "Скрипты", Prefixes: []string{"ScriptSettings", "ImportedScripts"}},
	{ID: "xml", Title: "Свой XML", Prefixes: []string{"Components", "ExtraXML"}},
}

func editorSectionKnown(id string) bool {
	for _, s := range editorSections {
		if s.ID == id {
			return true
		}
	}
	return false
}

type editorOption struct{ Value, Label string }

type editorLine struct {
	Label, Text string
	Danger      bool
}

type editorWarning struct{ Section, Text string }

// editorView is the editor page.
type editorView struct {
	Form     url.Values
	Section  string
	Sections []editorSection
	Summary  []editorLine
	Warnings []editorWarning
	XML      string
	XMLSize  int64
	Error    string
	// Opened file: SourceID with its report; Modified for an editor file
	// changed by hand since.
	SourceID    int64
	SourceTitle string
	SourceSHA   string
	MenuCount   int
	Report      *unattend.ImportReport
	// Saved variant defaults.
	Title     string
	Folder    string
	CanSave   bool
	WipesDisk bool
	AddRow    string
	Presets   []editorOption
	// Choices for the form, as upstream names them.
	ImageLanguages, Locales, Keyboards, Geos, TimeZones, Editions, Bloatwares, DesktopIcons, StartFolders []editorOption
	Effects, StickyKeys, MachineNames                                                                     []string
}

// PrevSection and NextSection are the neighbours of the shown section.
func (v *editorView) PrevSection() string { return v.neighbour(-1) }
func (v *editorView) NextSection() string { return v.neighbour(1) }

func (v *editorView) neighbour(step int) string {
	for i, s := range editorSections {
		if s.ID == v.Section {
			j := (i + step + len(editorSections)) % len(editorSections)
			return editorSections[j].ID
		}
	}
	return editorSections[0].ID
}

// Rows lists row numbers for a list field: the rows in the form, at least
// min, and one more when «ещё строка» asked for it.
func (v *editorView) Rows(path string, min int) []int {
	count := 0
	prefix := path + "."
	for k := range v.Form {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if i := strings.IndexByte(rest, '.'); i > 0 {
			if n, err := strconv.Atoi(rest[:i]); err == nil && n+1 > count {
				count = n + 1
			}
		}
	}
	if count < min {
		count = min
	}
	if v.AddRow == path {
		count++
	}
	rows := make([]int, count)
	for i := range rows {
		rows[i] = i
	}
	return rows
}

// V is the form value of a field.
func (v *editorView) V(path string) string { return v.Form.Get(path) }

// On reports a set checkbox.
func (v *editorView) On(path string) bool { s := v.Form.Get(path); return s == "true" || s == "on" }

// Is reports whether a field has the value (a selected option or radio).
func (v *editorView) Is(path, value string) bool { return v.Form.Get(path) == value }

// Has reports whether a set field contains the value.
func (v *editorView) Has(path, value string) bool {
	for _, s := range v.Form[path] {
		if s == value {
			return true
		}
	}
	return false
}

// Row builds the path of a field in a list row.
func (v *editorView) Row(path string, i int, field string) string {
	return path + "." + strconv.Itoa(i) + "." + field
}

// Mapped reports whether a key-value row list holds key with value true;
// unknown reports whether the key is absent.
func (v *editorView) Mapped(path, key string) string {
	for _, i := range v.Rows(path, 0) {
		if v.V(v.Row(path, i, "Key")) == key {
			if v.On(v.Row(path, i, "Value")) {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

var editorGroupTitles = map[string]string{
	"language": "Язык и регион", "timezone": "Часовой пояс", "computer-name": "Имя компьютера", "edition": "Редакция и ключ",
	"activation-key": "Ключ активации", "bypass-checks": "Обход проверок", "configuration-set": "Папка $OEM$",
	"accounts": "Учётные записи", "express-settings": "Конфиденциальность", "eula": "Лицензия", "wifi": "Wi-Fi",
	"online-accounts": "Экран учётки Microsoft", "scripts": "Скрипты",
}

// GroupTitle names a recognized group of an opened file.
func (v *editorView) GroupTitle(key string) string {
	if t, ok := editorGroupTitles[key]; ok {
		return t
	}
	return key
}

// KeptShort lists the kept elements by their top element in a component,
// with how many there are: «OOBE · 3».
func (v *editorView) KeptShort() []string {
	if v.Report == nil {
		return nil
	}
	var order []string
	count := map[string]int{}
	for _, item := range v.Report.KeptAsXML {
		parts := strings.Split(item, "/")
		name := parts[len(parts)-1]
		if len(parts) > 2 {
			name = parts[2]
		}
		if count[name] == 0 {
			order = append(order, name)
		}
		count[name]++
	}
	out := make([]string, len(order))
	for i, name := range order {
		out[i] = name
		if count[name] > 1 {
			out[i] = fmt.Sprintf("%s · %d", name, count[name])
		}
	}
	return out
}

// Fragment shows a kept XML element in one line.
func (v *editorView) Fragment(xml string) string {
	s := strings.Join(strings.Fields(xml), " ")
	if len([]rune(s)) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}

var editorPresets = []editorOption{
	{Value: "windows", Label: "Как Windows по умолчанию"},
	{Value: "language-key", Label: "Только язык и ключ — диск вручную"},
	{Value: "lab", Label: "Лаборатория: локальная учётка, автовход"},
}

func editorPreset(id string) (unattend.Config, bool) {
	c := unattend.Default()
	russian := unattend.UnattendedLanguageSettings{
		ImageLanguage:      "ru-RU",
		LocaleAndKeyboard:  unattend.LocaleAndKeyboard{Locale: "ru-RU", Keyboard: "00000419"},
		LocaleAndKeyboard2: &unattend.LocaleAndKeyboard{Locale: "en-US", Keyboard: "00000409"},
		GeoLocation:        "203",
	}
	switch id {
	case "windows":
	case "language-key":
		c.LanguageSettings = russian
		c.PESettings = unattend.DefaultPESettings{EditionSettings: unattend.UnattendedEditionSettings{Edition: "pro"}, BypassRequirementsCheck: false}
		c.ExpressSettings = "Interactive"
		c.KeepSensitiveFiles = true
	case "lab":
		c.LanguageSettings = russian
		c.TimeZoneSettings = unattend.ExplicitTimeZoneSettings{TimeZone: "Russian Standard Time"}
		c.PESettings = unattend.DefaultPESettings{EditionSettings: unattend.UnattendedEditionSettings{Edition: "pro"}, BypassRequirementsCheck: true}
		c.AccountSettings = unattend.UnattendedAccountSettings{Accounts: []unattend.Account{{Name: "admin", Group: "Administrators"}}, AutoLogonSettings: unattend.OwnAutoLogonSettings{}, ObscurePasswords: true}
		c.WifiSettings = unattend.SkipWifiSettings{}
		c.BypassNetworkCheck = true
		c.LaunchToThisPC = true
		c.ShowFileExtensions = true
	default:
		return unattend.Config{}, false
	}
	return c, true
}

// editorControlFields are form fields of the page, not of the settings.
var editorControlFields = map[string]bool{"csrf": true, "section": true, "do": true, "preset": true, "profile_id": true, "title": true, "folder": true, "source_id": true, "source_title": true, "add": true, "save_mode": true}

// editorForm separates the settings fields of a posted form and drops rows
// marked for removal.
func editorForm(posted url.Values) url.Values {
	form := url.Values{}
	var removed []string
	for k, v := range posted {
		if editorControlFields[k] {
			continue
		}
		if strings.HasSuffix(k, ".remove") {
			if len(v) > 0 && v[0] == "true" {
				removed = append(removed, strings.TrimSuffix(k, "remove"))
			}
			continue
		}
		form[k] = v
	}
	for _, prefix := range removed {
		for k := range form {
			if strings.HasPrefix(k, prefix) {
				delete(form, k)
			}
		}
	}
	return form
}

func (s *Server) newEditorView(r *http.Request, form url.Values) *editorView {
	info := requestInfo(r)
	v := &editorView{Form: form, Section: "language", CanSave: Allowed(info.User.Role, "images"), Folder: "Windows", Presets: editorPresets}
	if book, ok := s.Store.(MachineBook); !ok {
	} else if machines, err := book.Machines(r.Context()); err == nil {
		seen := map[string]bool{}
		for _, m := range machines {
			if m.Name != "" && !seen[m.Name] && len(v.MachineNames) < 500 {
				seen[m.Name] = true
				v.MachineNames = append(v.MachineNames, m.Name)
			}
		}
		sort.Strings(v.MachineNames)
	}
	if d, err := unattend.LoadData(); err == nil {
		list := func(ids []string, name func(string) string) []editorOption {
			out := make([]editorOption, 0, len(ids))
			for _, id := range ids {
				out = append(out, editorOption{Value: id, Label: name(strings.ToLower(id))})
			}
			return out
		}
		v.ImageLanguages = list(d.ImageLanguageList, func(k string) string { return d.ImageLanguages[k].DisplayName })
		v.Locales = list(d.LocaleList, func(k string) string { return d.UserLocales[k].DisplayName })
		v.Keyboards = list(d.KeyboardList, func(k string) string { return d.KeyboardIdentifiers[k].DisplayName })
		v.Geos = list(d.GeoList, func(k string) string { return d.GeoLocations[k].DisplayName })
		v.TimeZones = list(d.TimeOffsetList, func(k string) string { return d.TimeOffsets[k].DisplayName })
		v.Editions = list(d.EditionList, func(k string) string { return d.WindowsEditions[k].DisplayName })
		v.Bloatwares = list(d.BloatwareList, func(k string) string { return d.Bloatwares[k].DisplayName })
		v.DesktopIcons = list(d.DesktopIconList, func(k string) string { return d.DesktopIcons[k].DisplayName })
		v.StartFolders = list(d.StartFolderList, func(k string) string { return d.StartFolders[k].DisplayName })
	}
	v.Effects = []string{"ControlAnimations", "AnimateMinMax", "TaskbarAnimations", "DWMAeroPeekEnabled", "MenuAnimation", "TooltipAnimation", "SelectionFade", "DWMSaveThumbnailEnabled", "CursorShadow", "ListviewShadow", "ThumbnailsOrIcon", "ListviewAlphaSelect", "DragFullWindows", "ComboBoxAnimation", "FontSmoothing", "ListBoxSmoothScrolling", "DropShadow"}
	v.StickyKeys = []string{"HotKeyActive", "Indicator", "TriState", "TwoKeysOff", "AudibleFeedback", "HotKeySound"}
	return v
}

// evaluate builds the file and fills the summary, marks and warnings.
// It returns the XML, or nil with v.Error set.
func (v *editorView) evaluate(lang string, cfg *unattend.Config, formErr error) []byte {
	v.Sections = append([]editorSection(nil), editorSections...)
	if formErr != nil {
		v.Error = editorFormError(lang, formErr)
		return nil
	}
	d, err := unattend.LoadData()
	if err != nil {
		v.Error = translate(lang, "Справочники генератора недоступны")
		return nil
	}
	xml, warnings, err := unattend.Build(d, *cfg)
	if err != nil {
		var ce *unattend.ConfigurationError
		if errors.As(err, &ce) {
			v.Error = fmt.Sprintf(translate(lang, "Генератор не принял настройки: %s"), ce.Message)
		} else {
			v.Error = fmt.Sprintf(translate(lang, "Генератор не принял настройки: %s"), err.Error())
		}
	}
	current, defaults := unattend.FormValues(*cfg), unattend.FormValues(unattend.Default())
	for i := range v.Sections {
		sec := &v.Sections[i]
		changed := false
		for _, prefix := range sec.Prefixes {
			changed = changed || !sameValues(current, defaults, prefix)
		}
		switch {
		case sec.ID == "apps" && len(cfg.Bloatwares) > 0:
			sec.Mark = strconv.Itoa(len(cfg.Bloatwares))
		case sec.ID == "xml" && len(cfg.ExtraXML)+len(cfg.Components) > 0:
			sec.Mark = strconv.Itoa(len(cfg.ExtraXML) + len(cfg.Components))
		case sec.ID == "scripts" && len(cfg.ScriptSettings.Scripts)+len(cfg.ImportedScripts) > 0:
			sec.Mark = strconv.Itoa(len(cfg.ScriptSettings.Scripts) + len(cfg.ImportedScripts))
		case changed:
			sec.Mark = "changed"
		}
	}
	v.Summary = editorSummary(lang, d, *cfg)
	v.Warnings = editorWarnings(lang, *cfg)
	for _, w := range warnings {
		v.Warnings = append(v.Warnings, editorWarning{Section: "xml", Text: fmt.Sprintf(translate(lang, "Свой XML: %s"), w)})
	}
	for i := range v.Sections {
		for _, w := range v.Warnings {
			if w.Section == v.Sections[i].ID {
				v.Sections[i].Questions++
			}
		}
	}
	if err != nil {
		return nil
	}
	wipes, _ := AnswerWipesDisk(xml)
	v.WipesDisk = wipes
	v.XMLSize = int64(len(xml))
	v.XML = editorXMLForDisplay(xml)
	return xml
}

var editorCDATA = regexp.MustCompile(`(?s)<!\[CDATA\[.*?\]\]>`)

// editorXMLForDisplay shortens the stored settings, which are one long line.
func editorXMLForDisplay(xml []byte) string {
	s := strings.ReplaceAll(string(xml), "\r\n", "\n")
	return editorCDATA.ReplaceAllString(s, "<![CDATA[…]]>")
}

func sameValues(a, b url.Values, prefix string) bool {
	keys := map[string]bool{}
	for k := range a {
		if k == prefix || strings.HasPrefix(k, prefix+".") {
			keys[k] = true
		}
	}
	for k := range b {
		if k == prefix || strings.HasPrefix(k, prefix+".") {
			keys[k] = true
		}
	}
	for k := range keys {
		if strings.Join(a[k], "\x00") != strings.Join(b[k], "\x00") {
			return false
		}
	}
	return true
}

func editorFormError(lang string, err error) string {
	var fe *unattend.FormError
	if errors.As(err, &fe) {
		field := fe.Path
		if i := strings.LastIndexByte(field, '.'); i >= 0 {
			field = field[i+1:]
		}
		if fe.Problem == "number" {
			return fmt.Sprintf(translate(lang, "Нужно целое число в поле %s"), field)
		}
		return fmt.Sprintf(translate(lang, "Не выбран вариант в поле %s"), field)
	}
	return translate(lang, "Не удалось прочитать форму")
}

func editorSummary(lang string, d *unattend.Data, c unattend.Config) []editorLine {
	t := func(s string) string { return translate(lang, s) }
	name := func(m func(string) string, id string) string {
		if n := m(strings.ToLower(id)); n != "" {
			return n
		}
		return id
	}
	var out []editorLine
	add := func(label, text string, danger bool) {
		out = append(out, editorLine{Label: t(label), Text: text, Danger: danger})
	}

	switch s := c.LanguageSettings.(type) {
	case unattend.UnattendedLanguageSettings:
		keyboards := []string{name(func(k string) string { return keyboardName(d, k) }, s.LocaleAndKeyboard.Keyboard)}
		for _, p := range []*unattend.LocaleAndKeyboard{s.LocaleAndKeyboard2, s.LocaleAndKeyboard3} {
			if p != nil {
				keyboards = append(keyboards, name(func(k string) string { return keyboardName(d, k) }, p.Keyboard))
			}
		}
		image := s.ImageLanguage
		if l, ok := d.ImageLanguages[strings.ToLower(s.ImageLanguage)]; ok {
			image = l.DisplayName
		}
		geo := s.GeoLocation
		if g, ok := d.GeoLocations[strings.ToLower(s.GeoLocation)]; ok {
			geo = g.DisplayName
		}
		add("Язык", fmt.Sprintf(t("%s; раскладки: %s; регион: %s"), image, strings.Join(keyboards, ", "), geo), false)
	default:
		add("Язык", t("спросит установщик"), false)
	}

	switch pe := c.PESettings.(type) {
	case unattend.GeneratePESettings:
		text := t("pe.cmd размечает диск сам")
		if ps, ok := pe.PartitionSettings.(unattend.UnattendedPartitionSettings); ok {
			disk := t("подобранный по условиям")
			switch td := ps.TargetDiskSettings.(type) {
			case unattend.FixedTargetDiskSettings:
				disk = strconv.Itoa(td.Index)
			case unattend.InteractiveTargetDiskSettings:
				disk = t("выбранный на экране")
			}
			text = fmt.Sprintf(t("Стирает диск %s без вопросов"), disk)
		}
		add("Диск", text, true)
	case unattend.ScriptPESetttings:
		add("Диск", t("свой сценарий pe.cmd вместо установщика"), true)
	case unattend.DefaultPESettings:
		text := t("выберете в установщике")
		for _, x := range c.ExtraXML {
			if strings.Contains(x.XML, "DiskConfiguration") || strings.Contains(x.XML, "diskpart") {
				text = t("разметка из исходного файла (свой XML)")
			}
		}
		add("Диск", text, text != t("выберете в установщике"))
		if pe.BypassRequirementsCheck {
			add("Проверки", t("без проверок TPM, Secure Boot и памяти"), false)
		}
		switch e := pe.EditionSettings.(type) {
		case unattend.UnattendedEditionSettings:
			add("Windows", fmt.Sprintf(t("%s, общий ключ установки (не активирует)"), name(func(k string) string { return editionName(d, k) }, e.Edition)), false)
		case unattend.CustomEditionSettings:
			key := e.ProductKey.Value
			if len(key) > 5 {
				key = "…" + key[len(key)-5:]
			}
			add("Windows", fmt.Sprintf(t("свой ключ %s"), key), false)
		case unattend.FirmwareEditionSettings:
			add("Windows", t("ключ из прошивки компьютера"), false)
		default:
			add("Windows", t("редакцию и ключ спросит установщик"), false)
		}
	}

	switch cn := c.ComputerNameSettings.(type) {
	case unattend.CustomComputerNameSettings:
		add("Имя", cn.Name, false)
	case unattend.ScriptComputerNameSettings:
		add("Имя", t("из скрипта"), false)
	case unattend.PromptComputerNameSettings:
		add("Имя", t("спросит при установке"), false)
	default:
		add("Имя", t("случайное"), false)
	}
	if tz, ok := c.TimeZoneSettings.(unattend.ExplicitTimeZoneSettings); ok {
		zone := tz.TimeZone
		if z, ok := d.TimeOffsets[strings.ToLower(tz.TimeZone)]; ok {
			zone = z.DisplayName
		}
		add("Время", zone, false)
	}

	switch a := c.AccountSettings.(type) {
	case unattend.UnattendedAccountSettings:
		var names []string
		for _, acc := range a.Accounts {
			names = append(names, acc.Name)
		}
		who := t("никто")
		switch a.AutoLogonSettings.(type) {
		case unattend.BuiltinAutoLogonSettings:
			who = "Administrator"
		case unattend.OwnAutoLogonSettings:
			for _, acc := range a.Accounts {
				if acc.Group == "Administrators" {
					who = acc.Name
					break
				}
			}
		}
		add("Вход", fmt.Sprintf(t("учётки: %s; входит сам: %s"), strings.Join(names, ", "), who), false)
	case unattend.InteractiveLocalAccountSettings:
		add("Вход", t("локальную учётку спросит установщик"), false)
	default:
		add("Вход", t("учётку Microsoft спросит установщик"), false)
	}

	if n := len(c.Bloatwares); n > 0 {
		var names []string
		for i, id := range c.Bloatwares {
			if i == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, name(func(k string) string {
				if b, ok := d.Bloatwares[k]; ok {
					return b.DisplayName
				}
				return ""
			}, id))
		}
		add("Программы", fmt.Sprintf(t("убирает %d: %s"), n, strings.Join(names, ", ")), false)
	}
	if n := len(c.ScriptSettings.Scripts) + len(c.ImportedScripts); n > 0 {
		add("Скрипты", fmt.Sprintf(t("%d шт."), n), false)
	}
	if n := len(c.ExtraXML) + len(c.Components); n > 0 {
		add("Свой XML", fmt.Sprintf(t("%d элементов"), n), false)
	}
	return out
}

func keyboardName(d *unattend.Data, k string) string {
	if kb, ok := d.KeyboardIdentifiers[k]; ok {
		return kb.DisplayName
	}
	return ""
}

func editionName(d *unattend.Data, k string) string {
	if e, ok := d.WindowsEditions[k]; ok {
		return e.DisplayName
	}
	return ""
}

func editorWarnings(lang string, c unattend.Config) []editorWarning {
	t := func(s string) string { return translate(lang, s) }
	var out []editorWarning
	if a, ok := c.AccountSettings.(unattend.UnattendedAccountSettings); ok {
		if c.KeepSensitiveFiles && len(a.Accounts) > 0 {
			out = append(out, editorWarning{"accounts", t("Файл с паролями останется на установленной Windows (C:\\Windows\\Panther). Снимите «Оставить файл ответов на диске», если это не нужно.")})
		}
		if !a.ObscurePasswords && len(a.Accounts) > 0 {
			out = append(out, editorWarning{"accounts", t("Пароли записаны в файл открытым текстом.")})
		}
	}
	var off []string
	if c.DisableUac {
		off = append(off, "UAC")
	}
	if c.DisableSmartScreen {
		off = append(off, "SmartScreen")
	}
	if c.IsDefenderDisabled() {
		off = append(off, "Defender")
	}
	if c.DisableSac {
		off = append(off, "Smart App Control")
	}
	if c.DisableCoreIsolation {
		off = append(off, t("изоляция ядра"))
	}
	if len(off) > 0 {
		out = append(out, editorWarning{"optimize", fmt.Sprintf(t("Отключает защиту Windows: %s."), strings.Join(off, ", "))})
	}
	return out
}

// answerEditorNew opens the editor with default settings.
func (s *Server) answerEditorNew(w http.ResponseWriter, r *http.Request) {
	cfg := unattend.Default()
	v := s.newEditorView(r, unattend.FormValues(cfg))
	if pageError(r) != "" {
		// Re-shown after an error: keep what was posted.
		if form, ok := r.Context().Value(pageFormKey{}).(url.Values); ok {
			s.editorFromPost(v, form)
		}
		v.Error = pageError(r)
	}
	s.renderEditor(w, r, v)
}

// answerEditorEdit opens a stored answer file.
func (s *Server) answerEditorEdit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Неверный скрипт", http.StatusBadRequest)
		return
	}
	answer, err := s.Store.WindowsAnswer(r.Context(), id)
	if err != nil {
		http.Error(w, "Скрипт не найден", http.StatusNotFound)
		return
	}
	reader, ok := s.WindowsAnswers.(WindowsAnswerReader)
	if !ok {
		http.Error(w, "Подготовка скриптов недоступна", http.StatusServiceUnavailable)
		return
	}
	data, err := reader.WindowsAnswerXML(r.Context(), agent.WindowsAnswerCheck{ID: answer.ID, SHA256: answer.SHA256})
	if err != nil {
		http.Error(w, "Не удалось прочитать файл ответов у агента", http.StatusServiceUnavailable)
		return
	}
	d, err := unattend.LoadData()
	if err != nil {
		http.Error(w, "Справочники генератора недоступны", http.StatusServiceUnavailable)
		return
	}
	cfg, report, err := unattend.Open(d, data)
	if err != nil {
		http.Error(w, fmt.Sprintf("Файл ответов не открывается в редакторе: %s", err.Error()), http.StatusUnprocessableEntity)
		return
	}
	v := s.newEditorView(r, unattend.FormValues(cfg))
	v.SourceID, v.SourceTitle, v.SourceSHA, v.Report = answer.ID, answer.Title, answer.SHA256, &report
	v.Title, v.Folder = answer.Title, answer.Folder
	s.renderEditor(w, r, v)
}

func (s *Server) renderEditor(w http.ResponseWriter, r *http.Request, v *editorView) {
	if v.SourceID > 0 {
		if v.SourceSHA == "" {
			if existing, err := s.Store.WindowsAnswer(r.Context(), v.SourceID); err == nil {
				v.SourceSHA = existing.SHA256
			}
		}
		v.MenuCount = s.loadAnswerFileUsage(r.Context()).Windows[v.SourceID].Published
	}
	lang := normalizeLanguage(requestInfo(r).User.Language)
	if v.Error == "" {
		cfg, err := unattend.ParseFormValues(v.Form)
		v.evaluate(lang, &cfg, err)
	} else {
		cfg, err := unattend.ParseFormValues(v.Form)
		keep := v.Error
		v.evaluate(lang, &cfg, err)
		v.Error = keep
	}
	data := pageData{Info: requestInfo(r), Editor: v, Message: r.URL.Query().Get("message")}
	render(w, "answer_editor", data)
}

// editorFromPost reads the page fields of a posted form into the view.
func (s *Server) editorFromPost(v *editorView, posted url.Values) {
	v.Form = editorForm(posted)
	if sec := posted.Get("section"); editorSectionKnown(sec) {
		v.Section = sec
	}
	if posted.Has("title") {
		v.Title = posted.Get("title")
	}
	if posted.Has("folder") {
		v.Folder = posted.Get("folder")
	}
	if id, err := strconv.ParseInt(posted.Get("source_id"), 10, 64); err == nil && id > 0 {
		v.SourceID, v.SourceTitle = id, posted.Get("source_title")
	}
	v.AddRow = posted.Get("add")
}

// answerEditorPost handles everything but saving: switching sections,
// presets, new rows, the JSON preview and the download.
func (s *Server) answerEditorPost(w http.ResponseWriter, r *http.Request) {
	lang := normalizeLanguage(requestInfo(r).User.Language)
	v := s.newEditorView(r, nil)
	s.editorFromPost(v, r.PostForm)
	if id := r.PostForm.Get("preset"); id != "" {
		if cfg, ok := editorPreset(id); ok {
			v.Form = unattend.FormValues(cfg)
		}
	}
	switch r.PostForm.Get("do") {
	case "preview":
		cfg, err := unattend.ParseFormValues(v.Form)
		v.evaluate(lang, &cfg, err)
		marks := map[string]any{}
		for _, sec := range v.Sections {
			marks[sec.ID] = map[string]any{"mark": sec.Mark, "questions": sec.Questions}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"error": v.Error, "summary": v.Summary, "warnings": v.Warnings, "xml": v.XML, "size": v.XMLSize, "wipes": v.WipesDisk, "marks": marks})
		return
	case "download":
		cfg, err := unattend.ParseFormValues(v.Form)
		if xml := v.evaluate(lang, &cfg, err); xml != nil {
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Content-Disposition", `attachment; filename="autounattend.xml"`)
			_, _ = w.Write(xml)
			return
		}
	}
	s.renderEditor(w, r, v)
}

// answerEditorSave stores the built file as a new answer variant of a
// profile. Editing an existing file never changes it.
func (s *Server) answerEditorSave(w http.ResponseWriter, r *http.Request) {
	lang := normalizeLanguage(requestInfo(r).User.Language)
	v := s.newEditorView(r, nil)
	s.editorFromPost(v, r.PostForm)
	cfg, err := unattend.ParseFormValues(v.Form)
	xml := v.evaluate(lang, &cfg, err)
	fail := func(status int, message string) {
		v.Error = translate(lang, message)
		w.WriteHeader(status)
		s.renderEditor(&headerlessWriter{w}, r, v)
	}
	if xml == nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.renderEditor(&headerlessWriter{w}, r, v)
		return
	}
	if len(xml) > maxWindowsAnswerBytes {
		fail(http.StatusRequestEntityTooLarge, "Файл больше 1 МБ: сохранить в библиотеку нельзя, скачать можно")
		return
	}
	if ValidateWindowsAnswer(xml) != nil {
		fail(http.StatusUnprocessableEntity, "Некорректный XML-файл скрипта")
		return
	}
	v.Title = strings.TrimSpace(v.Title)
	if !safeMenuText(v.Title, 80) {
		fail(http.StatusBadRequest, "Укажите название скрипта (до 80 символов)")
		return
	}
	s.windowsMu.Lock()
	defer s.windowsMu.Unlock()
	if s.WindowsAnswers == nil {
		fail(http.StatusServiceUnavailable, "Подготовка скриптов недоступна")
		return
	}
	saveMode := r.PostForm.Get("save_mode")
	var (
		variant WindowsAnswerVariant
		status  int
		message string
		updated bool
	)
	if v.SourceID > 0 && saveMode == "update" {
		variant, status, message = s.updateWindowsAnswer(r, v.SourceID, v.Title, xml)
		updated = true
	} else {
		variant, status, message = s.saveWindowsAnswer(r, v.Title, "", xml, true)
	}
	if status != 0 {
		fail(status, message)
		return
	}
	var msg string
	if updated {
		msg = fmt.Sprintf("Скрипт #%d «%s» обновлён", variant.ID, variant.Title)
	} else {
		msg = fmt.Sprintf("Файл ответов #%d сохранён", variant.ID)
		if v.Title != "" {
			msg = fmt.Sprintf("Файл ответов #%d «%s» сохранён", variant.ID, v.Title)
		}
	}
	http.Redirect(w, r, "/answers?message="+url.QueryEscape(msg), http.StatusSeeOther)
}
