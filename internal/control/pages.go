package control

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
	"pxe-platform/internal/images"
	"pxe-platform/internal/network"
)

type imageRow struct {
	Image       images.Image
	StorageName string
	CanPrepare  bool
	HasProfile  bool
	Unsupported bool
}

type menuVersionRow struct {
	MenuVersion
	Restorable bool
}

type pageData struct {
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
	Form                  url.Values
	Query                 string
	Tab                   string
	MACFilter             string
	Machines              []machineRow
	DHCPEvents            []agent.DHCPEvent
	DHCPError             string
	ProfileUse            map[int64]int
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
		"nav":             navigation,
		"formHas": func(form url.Values, key, value string) bool {
			for _, v := range form[key] {
				if v == value {
					return true
				}
			}
			return false
		},
		"dhcpKind":     dhcpKindLabel,
		"machineStage": machineStageLabel,
		"deref":        func(v *bool) bool { return v != nil && *v },
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
