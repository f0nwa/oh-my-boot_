package control

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"omb/internal/agent"
	"omb/internal/boot"
	"omb/internal/images"
	"omb/internal/network"
)

type imageRow struct {
	Image                          images.Image
	StorageName, Author            string
	Kind, Problem                  string
	USBProfileID, CleanupProfileID int64
	Downloads                      []imageDownload
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
	UbuntuEditor          *ubuntuAutoinstallEditorView
	UbuntuAutoinstalls    []UbuntuAutoinstallSummary
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
	BootStatus            string
	AgentStatus           string
	BootOK                bool
	AgentOK               bool
	EventError            string
	Events                []boot.Event
	Audit                 []AuditEvent
	Users                 []User
	Network               network.Config
	OldNetwork            network.Config
	Confirmation          string
	HasNetwork            bool
	Versions              []menuVersionRow
	Draft                 MenuDraft
	DraftRows             []menuDraftRow
	Changes               []MenuChange
	Installations         []installationRow
	PackageChoices        []packageChoice
	BootProfileChoices    []bootProfileChoice
	SelectedBootKind      string
	EditInstallationID    int64
	Form                  url.Values
	Query                 string
	Tab                   string
	MACFilter             string
	Machines              []machineRow
	Sessions              []bootSession
	Now                   *nowView
	Preview               *menuPreview
	InstallCounts         map[string]int
	SearchResults         []searchResult
	SessionIndex          int
	DHCPEvents            []agent.DHCPEvent
	DHCPError             string
	AnswerUse             map[int64]int
	ActiveVersion         int64
	ImageError            string
	Recovery              *agent.RecoveryStatus
	RecoveryError         string
	RecoveryMinutes       int
	Storages              []images.Storage
	ISOImages             []imageRow
	WindowsProfiles       []WindowsProfile
	WindowsAnswerVariants []WindowsAnswerVariant
	DriverPackages        []DriverPackage
	DriverConfigs         []DriverConfig
}

func storageKindLabel(kind string) string {
	if kind == "smb" {
		return "Сетевая папка SMB"
	}
	if kind == "mounted" {
		return "Смонтированный каталог"
	}
	return "Локальный каталог"
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
		"storageKind":     storageKindLabel,
		"storageState":    storageStateLabel,
		"roleLabel":       roleLabel,
		"roleDescription": roleDescription,
		"t":               func(text string) string { return translate(lang, text) },
		"lang":            func() string { return lang },
		"theme":           normalizeTheme,
		"nav":             navigation,
		"subnav":          subnav,
		"formHas": func(form url.Values, key, value string) bool {
			for _, v := range form[key] {
				if v == value {
					return true
				}
			}
			return false
		},
		"dhcpKind":            dhcpKindLabel,
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
		"add1":        func(i int) int { return i + 1 },
		"list":        func(s ...string) []string { return s },
		"filterList":  installationFilters,
		"size":        func(n int64) string { return humanSize(lang, n) },
		"arch":        isoArch,
		"usedPercent": usedPercent,
		"usedBytes":   func(total, free int64) int64 { return max(total-free, 0) },
	}).ParseFS(templateFS, "templates/*.html"))
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
