package control

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/images"
	"omb/internal/network"
)

type imageRow struct {
	AddedAt             *time.Time
	AddedVia            string
	Image               images.Image
	StorageName, Author string
	StorageState             string
	StorageUnavailable       bool
	StorageScanning          bool
	StorageUnknown           bool
	Maintenance              bool
	MaintenanceInherited     bool
	MaintenanceReason        string
	MaintenanceActiveClients int
	DisplayName              string
	Kind, Problem       string
	Arch                string
	ServerBytes         int64
	ServerNote          string
	CleanupProfileID    int64
	Downloads           []imageDownload
	DeleteBlockedReason string
	ActiveMigration     *images.MigrationJob
}

func (r imageRow) StorageBlocked() bool {
	return r.StorageUnavailable || r.StorageScanning || r.StorageUnknown
}

func (r imageRow) MatchesTab(tab string) bool {
	switch tab {
	case "used":
		return len(r.Downloads) > 0
	case "free":
		return len(r.Downloads) == 0
	case "attention":
		return !r.Image.Present || r.Problem != "" || r.StorageBlocked() || r.Maintenance
	default:
		return true
	}
}

func imageFilters() []installationFilter {
	return []installationFilter{
		{"Все", "", "all"},
		{"В загрузках", "used", "used"},
		{"Свободные", "free", "free"},
		{"Требуют внимания", "attention", "attention"},
	}
}

type imageDownload struct {
	ID    int64
	Title string
}

type menuVersionRow struct {
	MenuVersion
	Restorable         bool
	UnrestorableReason string
}

type pageData struct {
	AnswerChecksKnown     bool
	WindowsChecks         map[int64]AnswerCheck
	UbuntuChecks          map[int64]AnswerCheck
	WindowsProfileChecks  map[int64][]AnswerCheck
	SelectedWIM           agent.WindowsWIMInfo
	EventsAsync           bool
	UbuntuEditor          *ubuntuAutoinstallEditorView
	UbuntuAutoinstalls    []UbuntuAutoinstallSummary
	MaintenanceStorages   map[int64]bool
	SystemEvents          []SystemEvent
	SystemErrorCount      int
	SystemSev             string
	SystemCat             string
	SystemEventError      string
	Editor                *editorView
	USB                   *usbView
	Journal               *journalView
	Machine               *machineCard
	MachineList           *machineList
	MachineImport         *machineImportPlan
	Info                  requestContext
	CSRF                  string
	Lang                  string
	Message               string
	Error                 string
	PasswordPolicy        PasswordPolicy
	FileServer            fileServerView
	SystemTime            *systemTimeView
	SystemTimeError       string
	NowTime               time.Time
	BootStatus            string
	AgentStatus           string
	BootOK                bool
	AgentOK               bool
	EventError            string
	Events                []boot.Event
	EventsHasMore         bool
	EventsNextTime        string
	EventsNextID          int64
	EventsNextOffset      int
	Audit                 []AuditEvent
	Users                 []User
	Network               network.Config
	OldNetwork            network.Config
	Confirmation          string
	HasNetwork            bool
	Versions              []menuVersionRow
	VersionsCount         int
	Draft                 MenuDraft
	DraftRows             []menuDraftRow
	Changes               []MenuChange
	Installations         []installationRow
	PackageChoices        []packageChoice
	BootProfileChoices    []bootProfileChoice
	SelectedBootKind      string
	EditInstallationID    int64
	EditInstallation      *installationEditorActions
	Form                  url.Values
	Query                 string
	Tab                   string
	Sort                  string
	Dir                   string
	MACFilter             string
	Machines              []machineRow
	Sessions              []bootSession
	Now                   *nowView
	Preview               *menuPreview
	InstallCounts         map[string]int
	ImageCounts           map[string]int
	HasAnyISOImages       bool
	SearchResults         []searchResult
	SessionIndex          int
	DHCPEvents            []agent.DHCPEvent
	DHCPError             string
	AnswerFileUsage       answerFileUsage
	ActiveVersion         int64
	ImageError            string
	Recovery              *agent.RecoveryStatus
	RecoveryError         string
	RecoveryMinutes       int
	RescanSettings        *images.CatalogSettings
	RescanMinutes         int
	Storages              []images.Storage
	ISOImages             []imageRow
	WindowsProfiles       []WindowsProfile
	WindowsAnswerVariants []WindowsAnswerVariant
	DriverPackages        []DriverPackage
	DriverUsage           driverUsage
	DriverConfigs         []DriverConfig
	Metrics               SystemSnapshot
	StorageSpace          *storageSpaceView
	catalogCache          *images.Catalog
}

func storageKindLabel(kind string) string {
	switch kind {
	case "smb":
		return "SMB"
	case "nfs":
		return "NFS"
	case "mounted":
		return "Смонтированный каталог"
	default:
		return "Локальный каталог"
	}
}

func storageKindDisplay(kind, nfsVersion string) string {
	switch kind {
	case "nfs":
		switch nfsVersion {
		case "4.1":
			return "NFSv4.1"
		case "3":
			return "NFSv3"
		default:
			return "NFS"
		}
	case "smb":
		return "SMB"
	case "mounted":
		return "Смонтированный каталог"
	default:
		return "Локальный каталог"
	}
}

func storageStateLabel(state string) string {
	switch state {
	case "ready":
		return "Доступно"
	case "scanning":
		return "Сканирование"
	case "unavailable":
		return "Недоступно"
	default:
		return "Не проверено"
	}
}

//go:embed templates
var templateFS embed.FS

// pagesByLanguage holds the admin templates parsed once per interface
// language, with t and lang bound to that language.
var pagesByLanguage = map[string]*template.Template{langRU: parsePages(langRU), langEN: parsePages(langEN)}

func parsePages(lang string) *template.Template {
	return template.Must(template.New("pages").Funcs(template.FuncMap{
		"storageKind":        storageKindLabel,
		"storageKindDisplay": storageKindDisplay,
		"storageState":       storageStateLabel,
		"roleLabel":          roleLabel,
		"roleDescription":    roleDescription,
		"t":                  func(text string) string { return translate(lang, text) },
		"lang":               func() string { return lang },
		"theme":              normalizeTheme,
		"nav":                navigation,
		"subnav":             subnav,
		"formHas": func(form url.Values, key, value string) bool {
			for _, v := range form[key] {
				if v == value {
					return true
				}
			}
			return false
		},
		"dhcpKind":            dhcpKindLabel,
		"sysEventSev":         systemEventSeverityLabel,
		"sysEventCat":         systemEventCategoryLabel,
		"sysEventLinkLabel":   systemEventLinkLabel,
		"machineStage":        machineStageLabel,
		"machineCol":          machineCol,
		"machineImportFields": func() []string { return []string{"name", "manufacturer", "product", "note"} },
		"deref":               func(v *bool) bool { return v != nil && *v },
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m
		},
		"add1":            func(i int) int { return i + 1 },
		"list":            func(s ...string) []string { return s },
		"filterList":      installationFilters,
		"imageFilterList": imageFilters,
		"size":            func(n int64) string { return humanSize(lang, n) },
		"hasEnoughSpace":  func(free, size int64) bool { return free >= size+(512<<20) },
		"arch":            isoArch,
		"usedPercent":     usedPercent,
		"usedBytes":       func(total, free int64) int64 { return max(total-free, 0) },
		"join":            joinStrings,
		"shortSHA":        shortSHA,
		"jsonAttr": func(v any) string {
			b, err := json.Marshal(v)
			if err != nil {
				return "{}"
			}
			return string(b)
		},
		"answerCheckSummary":     answerCheckCellSummary,
		"answerCheckMap":         answerCheckProfileMap,
		"readyWindowsAnswers":    filterReadyWindowsAnswers,
		"validatedUbuntuAnswers": filterValidatedUbuntuAutoinstalls,
		"bootChoicesForPage":     filterBootChoicesForPage,
		"stepMoreCount": func(total, limit int) int {
			if total > limit {
				return total - limit
			}
			return 0
		},
		"compatResult":         EvaluateWindowsAnswerCompatibility,
		"compatSummaryText":    func(c CompatibilityResult) string { return compatSummaryLabel(lang, c) },
		"selectedAnswerCompat": selectedAnswerCompatibility,
		"chipText":             func(s string) string { return chipText(lang, s) },
		"passwordState": func(u User, pol PasswordPolicy, now time.Time) PasswordState {
			if now.IsZero() {
				now = time.Now()
			}
			return EvaluatePasswordState(u, pol, now)
		},
		"passwordStatusLabel": func(st PasswordState) string { return formatPasswordStatusLabel(lang, st) },
		"passwordRuleLabel":   func(u User, pol PasswordPolicy) string { return formatPasswordRuleLabel(lang, u, pol) },
		"passwordWarnMessage": func(st PasswordState) string { return formatPasswordWarnMessage(lang, st) },
		"formatLocalDate": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.In(serverLocation()).Format("02.01.2006")
		},
		"formatLocalDateTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.In(serverLocation()).Format("02.01.2006 15:04")
		},
	}).ParseFS(templateFS, "templates/*.html"))
}

func compatSummaryLabel(lang string, c CompatibilityResult) string {
	translateAll := func(items []string) string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, translate(lang, item))
		}
		return strings.Join(out, "; ")
	}
	switch c.Status {
	case "incompatible":
		return translateAll(c.Errors)
	case "warning":
		return translateAll(c.Warnings)
	}
	return translate(lang, c.MatchedSummary)
}

func selectedAnswerCompatibility(form url.Values, list []WindowsAnswerVariant, wim agent.WindowsWIMInfo) CompatibilityResult {
	if form == nil || !wim.Known {
		return CompatibilityResult{}
	}
	raw := form.Get("answer_id")
	if raw == "" || raw == "0" {
		return CompatibilityResult{}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return CompatibilityResult{}
	}
	for _, a := range list {
		if a.ID == id && a.State == "ready" {
			return EvaluateWindowsAnswerCompatibility(a.Target, wim)
		}
	}
	return CompatibilityResult{}
}

func chipText(lang, s string) string {
	return translate(lang, s)
}

type answerCheckSummary struct {
	Known     bool
	Visible   []AnswerCheck
	MoreCount int
}

func answerCheckCellSummary(known bool, single AnswerCheck, byProfile []AnswerCheck) answerCheckSummary {
	if !known {
		return answerCheckSummary{}
	}
	list := byProfile
	if len(list) == 0 && single.ID > 0 {
		list = []AnswerCheck{single}
	}
	const maxVisible = 3
	if len(list) > maxVisible {
		return answerCheckSummary{Known: true, Visible: list[:maxVisible], MoreCount: len(list) - maxVisible}
	}
	return answerCheckSummary{Known: true, Visible: list}
}

func answerCheckProfileMap(all map[int64][]AnswerCheck, answerID int64) map[string]AnswerCheck {
	checks := all[answerID]
	if len(checks) == 0 {
		return nil
	}
	out := make(map[string]AnswerCheck, len(checks))
	for _, c := range checks {
		if c.WindowsProfileID > 0 {
			out[strconv.FormatInt(c.WindowsProfileID, 10)] = c
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterReadyWindowsAnswers(list []WindowsAnswerVariant) []WindowsAnswerVariant {
	var out []WindowsAnswerVariant
	for _, item := range list {
		if item.State == "ready" {
			out = append(out, item)
		}
	}
	return out
}

func filterValidatedUbuntuAutoinstalls(list []UbuntuAutoinstallSummary) []UbuntuAutoinstallSummary {
	var out []UbuntuAutoinstallSummary
	for _, item := range list {
		if item.State == "validated" {
			out = append(out, item)
		}
	}
	return out
}

func filterBootChoicesForPage(list []bootProfileChoice, editID int64, selected string) []bootProfileChoice {
	if editID == 0 {
		return list
	}
	var out []bootProfileChoice
	for _, item := range list {
		if item.Value == selected {
			out = append(out, item)
		}
	}
	return out
}

func shortSHA(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:4] + "…" + s[len(s)-4:]
}

func render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var out bytes.Buffer
	lang := normalizeLanguage(data.Lang)
	if data.Lang == "" {
		lang = normalizeLanguage(data.Info.User.Language)
	}
	for _, field := range []*string{&data.Message, &data.Error, &data.ImageError, &data.EventError, &data.BootStatus, &data.AgentStatus, &data.DHCPError} {
		*field = translate(lang, *field)
	}
	if err := pagesByLanguage[lang].ExecuteTemplate(&out, name, data); err != nil {
		http.Error(w, translate(lang, "Ошибка отображения страницы"), 500)
		return
	}
	_, _ = w.Write(out.Bytes())
}

func roleLabel(role string) string {
	switch role {
	case "admin":
		return "Администратор"
	case "operator":
		return "Оператор"
	case "observer":
		return "Наблюдатель"
	default:
		return role
	}
}

func roleDescription(role string) string {
	switch role {
	case "admin":
		return "Все действия, включая сеть, пользователей и роли"
	case "operator":
		return "Импорт, подготовка и публикация образов; просмотр служб и загрузок"
	case "observer":
		return "Только просмотр состояния, каталога и журналов"
	default:
		return ""
	}
}

// humanSize writes a file size in gigabytes or megabytes, with the decimal
// separator of the interface language.
func humanSize(lang string, n int64) string {
	if n <= 0 {
		return "—"
	}
	value, unit := float64(n)/(1<<30), "ГБ"
	switch {
	case n >= 1<<40:
		value, unit = float64(n)/(1<<40), "ТБ"
	case n < 1<<20:
		value, unit = float64(n)/(1<<10), "КБ"
	case n < 1<<30:
		value, unit = float64(n)/(1<<20), "МБ"
	}
	text := strconv.FormatFloat(value, 'f', 1, 64)
	if lang != langEN {
		text = strings.Replace(text, ".", ",", 1)
	}
	return text + " " + translate(lang, unit)
}

// usedPercent is how full a file system is, 0–100.
func usedPercent(total, free int64) int {
	if total <= 0 {
		return 0
	}
	return int(min(max((total-free)*100/total, 0), 100))
}

// isoArch names the architecture an ISO file name says it is for.
func isoArch(name string) string {
	if strings.Contains(strings.ToLower(path.Base(name)), "arm64") {
		return "arm64"
	}
	return "x64"
}

// SortCol creates a TableColumn for a sortable table header.
func (p pageData) SortCol(targetCol, label, defaultDir string) TableColumn {
	params := url.Values{}
	if p.Query != "" {
		params.Set("q", p.Query)
	}
	if p.Tab != "" {
		params.Set("show", p.Tab)
	}
	baseURL := p.Info.Path
	if baseURL == "" {
		baseURL = "/"
	}
	return MakeSortCol(baseURL, p.Sort, p.Dir, targetCol, label, defaultDir, params)
}
