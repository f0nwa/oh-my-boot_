package control

import (
	"fmt"
	"net/http"
	"omb/internal/autoinstall"
	"strings"
)

type ubuntuEditorIssue struct {
	Section  string `json:"section"`
	Field    string `json:"field"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
type ubuntuEditorSection struct{ ID, Title string }
type ubuntuSummaryItem struct {
	Label string `json:"label"`
	Value string `json:"value"`
}
type ubuntuKeyboardRow struct {
	Index           int
	Layout, Variant string
}

func ubuntuFormFromValues(v autoinstall.Values) autoinstall.Form {
	return autoinstall.Form{Locale: v.Locale, Keyboard: v.Keyboard, KeyboardVariant: v.KeyboardVariant, KeyboardToggle: v.KeyboardToggle, TimeZone: v.TimeZone, Hostname: v.Hostname, Username: v.Username, DiskMode: v.DiskMode, DiskSerial: v.DiskSerial, RealName: v.RealName, SourceID: v.SourceID, PostInstallScript: v.PostInstallScript, Shutdown: v.Shutdown, Drivers: v.Drivers, Codecs: v.Codecs, Packages: v.Packages, Network: v.Network, SSH: v.SSH, MirrorMode: v.MirrorMode, MirrorURL: v.MirrorURL}
}
func ubuntuSection(field string) string {
	switch {
	case strings.HasPrefix(field, "keyboard") || field == "locale" || field == "timezone":
		return "language"
	case field == "hostname" || field == "username" || field == "password" || field == "realname":
		return "user"
	case strings.HasPrefix(field, "disk"):
		return "disk"
	case field == "drivers" || field == "codecs":
		return "hardware"
	case field == "packages" || field == "mirror":
		return "software"
	case strings.HasPrefix(field, "network") || strings.HasPrefix(field, "ssh"):
		return "network"
	case field == "script" || field == "shutdown":
		return "finish"
	default:
		return "source"
	}
}
func (s *Server) prepareUbuntuView(r *http.Request, v *ubuntuAutoinstallEditorView) {
	if !v.HasForm {
		v.Form = ubuntuFormFromValues(v.Values)
	}
	v.Form.Password = ""
	v.Sections = []ubuntuEditorSection{{"source", "Файл и источник"}, {"language", "Язык и регион"}, {"user", "Пользователь"}, {"disk", "Целевой диск"}, {"hardware", "Оборудование"}, {"software", "Программы и репозиторий"}, {"network", "Сеть и SSH"}, {"finish", "Завершение установки"}}
	section := v.Section
	if section == "" {
		section = r.FormValue("section")
		if section == "" {
			section = r.FormValue("active_section")
		}
	}
	v.Section = "source"
	for _, x := range v.Sections {
		if x.ID == section {
			v.Section = section
		}
	}
	v.Tab = r.FormValue("tab")
	if v.Tab == "" {
		v.Tab = r.FormValue("active_tab")
	}
	if v.Tab != "yaml" && v.Tab != "issues" {
		v.Tab = "summary"
	}
	profiles, _ := s.Store.BootProfiles(r.Context())
	for _, profile := range profiles {
		if (profile.Kind == BootProfileUbuntuISO || profile.Kind == BootProfileUbuntuBuiltin) && profile.UbuntuSourceID > 0 && profile.UbuntuState == "ready" {
			v.ISOProfiles = append(v.ISOProfiles, profile)
		}
	}
	isoID, isoSHA, sourceErr := s.selectedUbuntuEditorSource(r)
	v.ISOSelected = isoID
	var catalog autoinstall.SourceCatalog
	var err error
	if sourceErr != nil {
		err = sourceErr
	} else if isoID > 0 {
		catalog, err = s.ubuntuSourceCatalogFor(r.Context(), isoID, isoSHA)
	} else if len(v.ISOProfiles) == 0 {
		catalog, err = s.ubuntuSourceCatalog(r.Context())
	} else {
		err = fmt.Errorf("ISO not selected")
	}
	if err == nil {
		v.Sources = catalog.Sources
		v.SourceDigest = catalog.ISOSHA256
	} else if sourceErr == nil && isoID == 0 && len(v.ISOProfiles) > 0 {
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "source", Message: "Выберите ISO, чтобы увидеть доступные наборы приложений.", Severity: "warning"})
	} else {
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "source", Message: "Метаданные ISO недоступны. Можно использовать настройки образа по умолчанию.", Severity: "warning"})
	}
	v.UnknownSource = v.Form.SourceID != ""
	for _, x := range v.Sources {
		if x.ID == v.Form.SourceID {
			v.UnknownSource = false
		}
	}
	layouts := strings.Split(v.Form.Keyboard, ",")
	variants := strings.Split(v.Form.KeyboardVariant, ",")
	for i := 0; i < max(4, min(len(layouts), 8)); i++ {
		row := ubuntuKeyboardRow{Index: i}
		if i < len(layouts) {
			row.Layout = layouts[i]
		}
		if i < len(variants) {
			row.Variant = variants[i]
		}
		v.Keyboards = append(v.Keyboards, row)
	}
	v.PackagesText = strings.Join(v.Form.Packages, "\n")
	v.DNSText = strings.Join(v.Form.Network.DNS, "\n")
	v.KeysText = strings.Join(v.Form.SSH.AuthorizedKeys, "\n")
	add := func(label, value string) {
		if value == "" {
			value = "—"
		}
		v.Summary = append(v.Summary, ubuntuSummaryItem{label, value})
	}
	source := "Как в ISO"
	if v.Form.SourceID != "" {
		source = v.Form.SourceID
		for _, x := range v.Sources {
			if x.ID == v.Form.SourceID {
				source = x.Name
			}
		}
	}
	add("Набор приложений", source)
	add("Имя компьютера", v.Form.Hostname)
	add("Имя пользователя", v.Form.Username)
	add("Полное имя", v.Form.RealName)
	add("Локаль", v.Form.Locale)
	add("Раскладки", v.Form.Keyboard)
	add("Часовой пояс IANA", v.Form.TimeZone)
	switch v.Form.DiskMode {
	case "serial":
		add("Очистка диска", v.Form.DiskSerial)
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "disk", Message: "Автоматическая разметка стирает диск с серийным номером:", Field: v.Form.DiskSerial, Severity: "warning"})
	case "default":
		add("Целевой диск", "Диск по умолчанию (выбор установщика)")
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "disk", Message: "Автоматическая разметка стирает диск, выбранный установщиком. Проверьте целевой диск перед публикацией.", Severity: "warning"})
	default:
		add("Целевой диск", "Выбрать диск вручную в установщике")
	}
	if v.Form.Network.Mode == "static" {
		add("Сеть", v.Form.Network.Address+" · "+v.Form.Network.MAC)
	} else {
		add("Сеть", "DHCP")
	}
	if v.Form.SSH.InstallServer {
		add("SSH-сервер", "Включён")
	} else {
		add("SSH-сервер", "Выключен")
	}
	if v.Form.Drivers {
		add("Сторонние драйверы", "Установить")
	}
	if v.Form.Codecs {
		add("Кодеки", "Установить")
	}
	add("Дополнительные пакеты", strings.Join(v.Form.Packages, ", "))
	switch v.Form.MirrorMode {
	case "offline":
		add("Репозиторий", "Без обращения к архивам пакетов")
	case "custom":
		add("Репозиторий", v.Form.MirrorURL)
	default:
		add("Репозиторий", "Как в ISO")
	}
	if len(v.Form.Packages) > 0 || v.Form.SSH.InstallServer || v.Form.Drivers || v.Form.Codecs {
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "software", Message: "Для выбранных компонентов нужен доступный репозиторий пакетов Ubuntu.", Severity: "warning"})
	}
	if v.Form.PostInstallScript != "" {
		v.Issues = append(v.Issues, ubuntuEditorIssue{Section: "finish", Message: "Скрипт выполняется от root и может обращаться к сети независимо от режима репозитория.", Severity: "warning"})
	}
	if v.Form.Shutdown == "poweroff" {
		add("После установки", "Выключить компьютер")
	} else {
		add("После установки", "Перезагрузить компьютер")
	}
	lang := requestInfo(r).User.Language
	for i := range v.Summary {
		v.Summary[i].Label = translate(lang, v.Summary[i].Label)
		v.Summary[i].Value = translate(lang, v.Summary[i].Value)
	}
	for i := range v.Issues {
		v.Issues[i].Message = translate(lang, v.Issues[i].Message)
	}
}
func (s *Server) ubuntuFormError(w http.ResponseWriter, r *http.Request, title string, form autoinstall.Form, err error) {
	field, _, _ := strings.Cut(err.Error(), ":")
	section := ubuntuSection(field)
	labels := map[string]string{"title": "Название", "locale": "Локаль", "keyboard": "Раскладки", "keyboard variant": "Вариант раскладки", "keyboard toggle": "Переключение раскладок", "timezone": "Часовой пояс IANA", "hostname": "Имя компьютера", "username": "Имя пользователя", "password": "Пароль создаваемого пользователя", "realname": "Полное имя", "disk mode": "Разметка", "disk serial": "Точный серийный номер диска", "source": "Набор приложений", "packages": "Дополнительные пакеты", "mirror": "Источник пакетов", "network": "Сеть установленной системы", "network mac": "MAC-адрес интерфейса", "network address": "IPv4-адрес с префиксом", "network gateway": "Шлюз IPv4", "network dns": "DNS", "ssh": "SSH-сервер", "ssh keys": "Публичные SSH-ключи — по одному в строке", "script": "Скрипт в установленной системе от root", "shutdown": "После установки"}
	label := labels[field]
	if label == "" {
		label = "Файл и источник"
	}
	message := fmt.Sprintf(translate(requestInfo(r).User.Language, "Проверьте поле «%s»."), translate(requestInfo(r).User.Language, label))
	v := &ubuntuAutoinstallEditorView{Title: title, Form: form, HasForm: true, Section: section, Message: message, Issues: []ubuntuEditorIssue{{Section: section, Field: field, Message: message, Severity: "error"}}}
	if _, ok := verifyAutoinstallPreview(r.FormValue("preview_token"), requestInfo(r).Token); ok {
		v.Token = r.FormValue("preview_token")
	}
	v.Status = http.StatusBadRequest
	s.renderUbuntuAutoinstall(w, r, v)
}
