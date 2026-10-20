package control

import (
	"bytes"
	"html/template"
	"net/http"

	"pxe-platform/internal/agent"
	"pxe-platform/internal/boot"
	"pxe-platform/internal/images"
	"pxe-platform/internal/network"
)

type imageRow struct {
	Image       images.Image
	StorageName string
	CanPrepare  bool
	Unsupported bool
}

type menuWindowsProfileRow struct {
	WindowsProfile
	Selected   bool
	MenuFolder string
}

type menuWindowsAnswerRow struct {
	WindowsAnswerVariant
	Selected   bool
	MenuFolder string
}

type menuDriverConfigRow struct {
	DriverConfig
	Selected   bool
	MenuFolder string
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
	EventError            string
	Events                []boot.Event
	Audit                 []AuditEvent
	Users                 []User
	Network               network.Config
	OldNetwork            network.Config
	Confirmation          string
	HasNetwork            bool
	Versions              []menuVersionRow
	MenuTitle             string
	MenuLabel             string
	MenuMessage           string
	MenuUbuntu            bool
	MenuFolderUbuntu      string
	HasMenuFolders        bool
	ImageError            string
	Recovery              *agent.RecoveryStatus
	RecoveryError         string
	RecoveryMinutes       int
	Storages              []images.Storage
	ISOImages             []imageRow
	WindowsProfiles       []WindowsProfile
	WindowsAnswerVariants []WindowsAnswerVariant
	MenuWindowsProfiles   []menuWindowsProfileRow
	MenuWindowsAnswers    []menuWindowsAnswerRow
	MenuDriverConfigs     []menuDriverConfigRow
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
	}).Parse(pagesTemplate))
}

const pagesTemplate = `
{{define "top"}}<!doctype html><html lang="{{lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>PXE Platform</title><style>
:root{font-family:system-ui,-apple-system,sans-serif;color:#172437;background:#f3f6fa}*{box-sizing:border-box}body{margin:0}header{background:#122640;color:white;padding:16px 24px;display:flex;align-items:center;gap:24px;flex-wrap:wrap}header strong{font-size:1.2rem}nav{display:flex;gap:14px;flex-wrap:wrap}a{color:#1868aa}header a{color:#d4e7fa;text-decoration:none}header span{margin-left:auto}main{max-width:1160px;margin:28px auto;padding:0 20px}h1{font-size:1.65rem;margin:0 0 20px}h2{font-size:1.2rem}section,.card{background:white;border:1px solid #dce4ed;border-radius:12px;padding:20px;margin:0 0 20px;box-shadow:0 2px 9px #14283a09}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px}.grid .card{margin:0}label{display:block;font-weight:600;margin:12px 0 5px}input,select{width:100%;max-width:520px;padding:10px;border:1px solid #a9b9c9;border-radius:7px;font:inherit}button{font:inherit;background:#0b6db8;color:white;border:0;border-radius:7px;padding:10px 16px;cursor:pointer;margin-top:12px}button.secondary{background:#495e75}table{border-collapse:collapse;width:100%;font-size:.92rem}td,th{text-align:left;padding:9px;border-bottom:1px solid #e4eaf0;vertical-align:top}th{color:#516277}small,.muted{color:#596e83}.error{color:#a52d2d}.notice{padding:12px;border-radius:8px;background:#eef6ff;margin-bottom:18px}.notice.error{background:#fdecec;border:1px solid #f2b8b8}.inline{display:inline}.inline button{margin:0;padding:5px 9px}pre{overflow:auto;background:#eef3f7;padding:14px;border-radius:7px}
</style></head><body><header><strong>PXE Platform</strong>{{if .Info.User.ID}}<nav><a href="/">{{t "Обзор"}}</a><a href="/events">{{t "Журнал"}}</a><a href="/network">{{t "Сеть"}}</a><a href="/menu">{{t "Меню"}}</a><a href="/images">{{t "Образы"}}</a><a href="/drivers">{{t "Драйверы"}}</a>{{if eq .Info.User.Role "admin"}}<a href="/users">{{t "Пользователи"}}</a><a href="/audit">{{t "Аудит"}}</a>{{end}}</nav><span><a href="/account" title="{{t "Личный кабинет"}}">{{.Info.User.Username}}</a> · {{t (roleLabel .Info.User.Role)}}</span><form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><button class="secondary">{{t "Выйти"}}</button></form>{{end}}</header><main>{{if .Error}}<div class="notice error" role="alert">{{t .Error}}</div>{{end}}{{if .Message}}<div class="notice">{{.Message}}</div>{{end}}{{end}}
{{define "bottom"}}</main></body></html>{{end}}
{{define "login"}}{{template "top" .}}<section style="max-width:430px;margin:80px auto"><h1>{{t "Вход в админку"}}</h1><form method="post" action="/login"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>{{t "Имя пользователя"}}</label><input name="username" autocomplete="username" required><label>{{t "Пароль"}}</label><input type="password" name="password" autocomplete="current-password" required><button>{{t "Войти"}}</button></form></section>{{template "bottom" .}}{{end}}
{{define "dashboard"}}{{template "top" .}}<h1>{{t "Обзор"}}</h1><div class="grid"><section class="card"><h2>Boot</h2><p>{{.BootStatus}}</p></section><section class="card"><h2>Network Agent</h2><p>{{.AgentStatus}}</p></section></div><section><h2>{{t "Последние запросы"}}</h2>{{if .EventError}}<p class="error">{{.EventError}}</p>{{else}}<table><tr><th>{{t "Время"}}</th><th>{{t "Тип"}}</th><th>{{t "Путь"}}</th><th>{{t "Клиент"}}</th></tr>{{range .Events}}<tr><td>{{.At.Format "02.01 15:04:05 UTC-07:00"}}</td><td>{{.Kind}}</td><td>{{.Path}}</td><td>{{.RemoteAddr}}</td></tr>{{end}}</table>{{end}}</section>{{template "bottom" .}}{{end}}
{{define "events"}}{{template "top" .}}<h1>{{t "Журнал запросов клиентов"}}</h1><section><p class="muted">{{t "Здесь показаны подтверждённые HTTP-запросы меню и файлов."}}</p>{{if .EventError}}<p class="error">{{.EventError}}</p>{{else}}<table><tr><th>{{t "Время"}}</th><th>{{t "Тип"}}</th><th>{{t "Путь"}}</th><th>{{t "Клиент"}}</th></tr>{{range .Events}}<tr><td>{{.At.Format "02.01.2006 15:04:05 UTC-07:00"}}</td><td>{{.Kind}}</td><td>{{.Path}}</td><td>{{.RemoteAddr}}</td></tr>{{end}}</table>{{end}}</section>{{template "bottom" .}}{{end}}
{{define "network"}}{{template "top" .}}<h1>{{t "PXE-сеть"}}</h1><section>{{if .HasNetwork}}<p>{{t "Активный режим:"}} <strong>{{.Network.Mode}}</strong> {{t "· интерфейс"}} <strong>{{.Network.Interface}}</strong> {{t "· адрес"}} {{.Network.ServerIP}}</p>{{else}}<p class="error">{{t "Активная конфигурация недоступна."}}</p>{{end}}</section>{{if eq .Info.User.Role "admin"}}<section><h2>{{t "Изменить конфигурацию"}}</h2><form method="post" action="/network/preview"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "Режим"}}</label><select name="mode"><option value="proxy" {{if eq .Network.Mode "proxy"}}selected{{end}}>{{t "Proxy DHCP — адреса выдаёт роутер"}}</option><option value="full" {{if eq .Network.Mode "full"}}selected{{end}}>{{t "Полный DHCP"}}</option></select><label>{{t "Интерфейс PXE"}}</label><input name="interface" value="{{.Network.Interface}}" required><label>{{t "IP сервера"}}</label><input name="server_ip" value="{{.Network.ServerIP}}" required><label>{{t "Подсеть CIDR"}}</label><input name="subnet" value="{{.Network.Subnet}}" required><label>{{t "Начало DHCP-пула"}}</label><input name="dhcp_start" value="{{.Network.DHCPStart}}"><label>{{t "Конец DHCP-пула"}}</label><input name="dhcp_end" value="{{.Network.DHCPEnd}}"><label>{{t "URL загрузочного меню"}}</label><input name="boot_url" value="{{.Network.BootURL}}" required><label>{{t "Каталог TFTP"}}</label><input name="tftp_root" value="{{.Network.TFTPRoot}}" required><button>{{t "Проверить изменения"}}</button></form></section>{{end}}{{template "bottom" .}}{{end}}
{{define "network_preview"}}{{template "top" .}}<h1>{{t "Подтверждение сети"}}</h1><section><p>{{t "Проверьте настройки перед применением. Это может изменить выдачу PXE в выбранном сегменте."}}</p><table><tr><th></th><th>{{t "Сейчас"}}</th><th>{{t "Будет"}}</th></tr><tr><td>{{t "Режим"}}</td><td>{{.OldNetwork.Mode}}</td><td>{{.Network.Mode}}</td></tr><tr><td>{{t "Интерфейс"}}</td><td>{{.OldNetwork.Interface}}</td><td>{{.Network.Interface}}</td></tr><tr><td>{{t "Адрес"}}</td><td>{{.OldNetwork.ServerIP}}</td><td>{{.Network.ServerIP}}</td></tr><tr><td>{{t "Подсеть"}}</td><td>{{.OldNetwork.Subnet}}</td><td>{{.Network.Subnet}}</td></tr><tr><td>{{t "Пул"}}</td><td>{{.OldNetwork.DHCPStart}}–{{.OldNetwork.DHCPEnd}}</td><td>{{.Network.DHCPStart}}–{{.Network.DHCPEnd}}</td></tr></table><form method="post" action="/network/apply"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><input type="hidden" name="confirmation" value="{{.Confirmation}}"><input type="hidden" name="mode" value="{{.Network.Mode}}"><input type="hidden" name="interface" value="{{.Network.Interface}}"><input type="hidden" name="server_ip" value="{{.Network.ServerIP}}"><input type="hidden" name="subnet" value="{{.Network.Subnet}}"><input type="hidden" name="dhcp_start" value="{{.Network.DHCPStart}}"><input type="hidden" name="dhcp_end" value="{{.Network.DHCPEnd}}"><input type="hidden" name="boot_url" value="{{.Network.BootURL}}"><input type="hidden" name="tftp_root" value="{{.Network.TFTPRoot}}"><button>{{t "Применить"}}</button> <a href="/network">{{t "Отмена"}}</a></form></section>{{template "bottom" .}}{{end}}
{{define "menu"}}{{template "top" .}}<h1>{{t "Загрузочное меню"}}</h1>{{if ne .Info.User.Role "observer"}}<section><h2>{{t "Новая версия"}}</h2><form method="post" action="/menu/publish"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "Заголовок"}}</label><input name="title" value="{{.MenuTitle}}" required><label>{{t "Диагностический пункт"}}</label><input name="label" value="{{.MenuLabel}}" required><label>{{t "Сообщение диагностики"}}</label><input name="message" value="{{.MenuMessage}}" required><label><input style="width:auto" type="checkbox" name="ubuntu" {{if .MenuUbuntu}}checked{{end}}> {{t "Ubuntu Desktop 26.04.1 (тестовая загрузка, ISO 6,5 ГБ)"}}</label>{{range .MenuWindowsProfiles}}<label><input style="width:auto" type="checkbox" name="winiso_{{.ID}}" {{if .Selected}}checked{{end}}> #{{.ID}} {{.Title}} · {{.State}}</label><label>{{t "Каталог для"}} {{.Title}}</label><input name="folder_winiso_{{.ID}}" value="{{.MenuFolder}}" placeholder="Windows">{{end}}{{range .MenuWindowsAnswers}}<label><input style="width:auto" type="checkbox" name="winanswer_{{.ID}}" {{if .Selected}}checked{{end}}> #{{.ID}} {{.Title}} {{t "· XML-сценарий ·"}} {{.State}}</label><label>{{t "Каталог для"}} {{.Title}}</label><input name="folder_winanswer_{{.ID}}" value="{{.MenuFolder}}" placeholder="Windows/Tests">{{end}}{{range .MenuDriverConfigs}}<label><input style="width:auto" type="checkbox" name="windriver_{{.ID}}" {{if .Selected}}checked{{end}}> #{{.ID}} {{.Title}} {{t "· драйверы WinPE ·"}} {{.State}} · {{range .PackageINFs}}{{.INFPath}} {{end}}</label><label>{{t "Каталог для"}} {{.Title}}</label><input name="folder_windriver_{{.ID}}" value="{{.MenuFolder}}" placeholder="Windows/Laptops">{{end}}<details {{if .HasMenuFolders}}open{{end}}><summary>{{t "Каталоги в PXE-меню"}}</summary><p class="muted">{{t "Укажите путь, например Windows/Установщики. Оставьте поле пустым для главного меню."}}</p><label>Ubuntu Desktop</label><input name="folder_ubuntu" value="{{.MenuFolderUbuntu}}" placeholder="Linux"></details><button>{{t "Опубликовать"}}</button></form></section>{{end}}<section><h2>{{t "Версии"}}</h2><table><tr><th>№</th><th>{{t "Время"}}</th><th>{{t "Название"}}</th><th>{{t "Автор"}}</th><th>SHA-256</th><th>{{t "Состояние"}}</th><th></th></tr>{{range .Versions}}<tr><td>#{{.ID}}</td><td>{{.At.Format "02.01.2006 15:04 UTC-07:00"}}</td><td>{{.Title}}</td><td>{{.Author}}</td><td><small>{{.SHA256}}</small></td><td>{{if .Active}}{{t "Активна"}}{{else}}{{if not .Restorable}}{{t "Удалённый пункт"}}{{end}}{{end}}</td><td>{{if and (not .Active) .Restorable (ne $.Info.User.Role "observer")}}<form method="post" action="/menu/rollback"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="secondary">{{t "Вернуть"}}</button></form>{{end}}</td></tr>{{end}}</table></section>{{template "bottom" .}}{{end}}
{{define "account"}}{{template "top" .}}<h1>{{t "Личный кабинет"}}</h1><section><h2>{{t "Учётная запись"}}</h2><table><tr><th>{{t "Имя пользователя"}}</th><td>{{.Info.User.Username}}</td></tr><tr><th>{{t "Роль"}}</th><td>{{t (roleLabel .Info.User.Role)}}</td></tr><tr><th>{{t "Доступ"}}</th><td>{{t (roleDescription .Info.User.Role)}}</td></tr></table></section><section><h2>{{t "Язык интерфейса"}}</h2><form method="post" action="/settings/language"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><input type="hidden" name="return" value="/account"><label>{{t "Язык"}}</label><select name="language"><option value="ru"{{if eq lang "ru"}} selected{{end}}>Русский</option><option value="en"{{if eq lang "en"}} selected{{end}}>English</option></select><button>{{t "Сохранить"}}</button></form></section><section><h2>{{t "Сменить пароль"}}</h2><p class="muted">{{t "После смены пароля сеансы на других устройствах будут завершены."}}</p><form method="post" action="/account/password"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "Текущий пароль"}}</label><input type="password" name="current_password" autocomplete="current-password" required><label>{{t "Новый пароль, от 12 символов"}}</label><input type="password" name="new_password" minlength="12" autocomplete="new-password" required data-password-generator><button>{{t "Сменить пароль"}}</button></form></section><script defer src="/assets/password.js"></script>{{template "bottom" .}}{{end}}
{{define "users"}}{{template "top" .}}<h1>{{t "Пользователи"}}</h1><section><h2>{{t "Добавить"}}</h2><form method="post" action="/users/create"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "Имя"}}</label><input name="username" required><label>{{t "Пароль, от 12 символов"}}</label><input type="password" name="password" minlength="12" autocomplete="new-password" required data-password-generator><label>{{t "Роль"}}</label><select name="role"><option value="observer">{{t "Наблюдатель"}}</option><option value="operator">{{t "Оператор"}}</option><option value="admin">{{t "Администратор"}}</option></select><button>{{t "Создать"}}</button></form></section><section><h2>{{t "Список"}}</h2><table><tr><th>{{t "Имя"}}</th><th>{{t "Роль и доступ"}}</th><th></th></tr>{{range .Users}}<tr><td>{{.Username}} <small>#{{.ID}}</small></td><td><form method="post" action="/users/change"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><select name="role"><option value="observer" {{if eq .Role "observer"}}selected{{end}}>{{t "Наблюдатель"}}</option><option value="operator" {{if eq .Role "operator"}}selected{{end}}>{{t "Оператор"}}</option><option value="admin" {{if eq .Role "admin"}}selected{{end}}>{{t "Администратор"}}</option></select><label><input style="width:auto" type="checkbox" name="enabled" {{if .Enabled}}checked{{end}}> {{t "Активен"}}</label><button>{{t "Сохранить"}}</button></form></td></tr>{{end}}</table></section><section><h2>{{t "Сменить пароль"}}</h2><form method="post" action="/users/reset-password"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "ID пользователя из списка"}}</label><input name="id" inputmode="numeric" required><label>{{t "Новый пароль, от 12 символов"}}</label><input name="password" type="password" minlength="12" autocomplete="new-password" required data-password-generator><button>{{t "Сменить пароль и завершить его сеансы"}}</button></form></section><script defer src="/assets/password.js"></script>{{template "bottom" .}}{{end}}
{{define "audit"}}{{template "top" .}}<h1>{{t "Аудит изменений"}}</h1><section><table><tr><th>{{t "Время"}}</th><th>{{t "Автор"}}</th><th>{{t "Действие"}}</th><th>{{t "Результат"}}</th><th>{{t "Детали"}}</th></tr>{{range .Audit}}<tr><td>{{.At.Format "02.01.2006 15:04:05 UTC-07:00"}}</td><td>{{.Actor}}</td><td>{{.Action}}</td><td>{{.Result}}</td><td>{{.Detail}}</td></tr>{{end}}</table></section>{{template "bottom" .}}{{end}}
{{define "images"}}{{template "top" .}}<h1>{{t "Образы ISO"}}</h1>
{{if eq .Info.User.Role "admin"}}<section><h2>{{t "Добавить сетевую папку SMB"}}</h2><p class="muted">{{t "Подключение только для чтения. Укажите прямой путь к папке с ISO."}}</p><form method="post" action="/images/smb"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>{{t "Путь к ISO"}}</label><input name="path" placeholder="//192.168.10.10/Downloads/iso" required><label>{{t "Пользователь"}}</label><input name="username" required autocomplete="off"><label>{{t "Пароль"}}</label><input name="password" type="password" required autocomplete="new-password"><details><summary>{{t "Домен, если нужен"}}</summary><input name="domain" autocomplete="off"></details><button>{{t "Подключить и найти ISO"}}</button></form></section>{{end}}
{{if .ImageError}}<section><p class="error">{{.ImageError}}</p></section>{{else}}
<section><h2>{{t "Хранилища"}}</h2>{{if .Storages}}<table><tr><th>{{t "Название"}}</th><th>{{t "Тип"}}</th><th>{{t "Состояние"}}</th><th>{{t "Расположение"}}</th><th>{{t "Действия"}}</th></tr>{{range .Storages}}<tr><td>{{.Name}}</td><td>{{t (storageKind .Kind)}}</td><td>{{t (storageState .State)}}{{if .Error}} · {{t .Error}}{{end}}</td><td>{{if eq .Kind "smb"}}//{{.Server}}/{{.Share}}{{if .Subpath}}/{{.Subpath}}{{end}}{{else}}{{.Root}}{{end}}</td><td>{{if ne $.Info.User.Role "observer"}}<form method="post" action="/images/scan"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="secondary">{{t "Обновить"}}</button></form>{{end}}{{if and (eq $.Info.User.Role "admin") (eq .Kind "smb")}}<details><summary>{{t "Повторить подключение"}}</summary><form method="post" action="/images/smb/{{.ID}}/retry"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><label>{{t "Пользователь"}}</label><input name="username" required autocomplete="off"><label>{{t "Пароль"}}</label><input name="password" type="password" required autocomplete="new-password"><label>{{t "Домен, если нужен"}}</label><input name="domain" autocomplete="off"><button>{{t "Подключить"}}</button></form></details><form method="post" action="/images/smb/{{.ID}}/remove"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Удалить"}}</button></form>{{end}}</td></tr>{{end}}</table>{{else}}<p>{{t "Хранилища пока не добавлены."}}</p>{{end}}</section><section><h2>{{t "Автоматическое восстановление подключений"}}</h2><p class="muted">{{t "Agent перезапускает упавшие подключения SMB, а затем ISO-профили Windows, например если NAS стал доступен позже PXE-сервера."}}</p>{{if .RecoveryError}}<p class="error">{{t .RecoveryError}}</p>{{else}}{{with .Recovery}}<p>{{if .Enabled}}{{t "Включено"}} · {{t "каждые"}} {{$.RecoveryMinutes}} {{t "мин"}}{{else}}{{t "Выключено"}}{{end}}</p><p>{{t "Последняя проверка"}}: {{if .LastRun.IsZero}}{{t "ещё не выполнялась"}}{{else}}{{.LastRun.Format "02.01.2006 15:04:05 UTC-07:00"}}{{end}}</p>{{if .Actions}}<ul>{{range .Actions}}<li><small>{{.}}</small></li>{{end}}</ul>{{end}}{{if .LastError}}<p class="error">{{.LastError}}</p>{{end}}{{if eq $.Info.User.Role "admin"}}<form method="post" action="/images/storage-recovery"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><label><input style="width:auto" type="checkbox" name="enabled" {{if .Enabled}}checked{{end}}> {{t "Включить восстановление"}}</label><label>{{t "Интервал проверки, минут (1–60)"}}</label><input name="interval_minutes" value="{{$.RecoveryMinutes}}" type="number" min="1" max="60" required><button>{{t "Сохранить"}}</button></form><form method="post" action="/images/storage-recovery/run"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Проверить сейчас"}}</button></form>{{end}}{{end}}{{end}}</section>
{{if ne .Info.User.Role "observer"}}<section><h2>{{t "Загрузить ISO"}}</h2><p class="muted">{{t "Выберите SMB-хранилище и существующую папку. Файл появится в каталоге после проверки. Публикация в PXE-меню выполняется отдельно."}}</p><form id="iso-upload-form" data-csrf="{{.Info.CSRF}}"><label>{{t "Хранилище"}}</label><select name="storage" required>{{range .Storages}}{{if and (eq .Kind "smb") (eq .State "ready")}}<option value="{{.ID}}">{{.Name}}</option>{{end}}{{end}}</select><label>{{t "Папка"}}</label><select name="folder"></select><label>{{t "Файл ISO (до 32 ГБ)"}}</label><input name="file" type="file" accept=".iso,application/x-iso9660-image" required><button type="submit">{{t "Загрузить"}}</button> <button type="button" class="secondary" id="iso-upload-cancel">{{t "Отменить"}}</button><p><progress id="iso-upload-progress" max="100" value="0" style="width:100%;max-width:520px"></progress></p><p id="iso-upload-status" role="status"></p></form></section><script defer src="/assets/iso-upload.js"></script>{{end}}
<section><h2>{{t "Найденные ISO"}}</h2><p class="muted">{{t "Для Windows x64 из SMB-хранилища можно создать отдельный загрузочный профиль. Название и каталог в PXE-меню указывайте латиницей; формат ISO проверяется при подготовке."}}</p>{{if .ISOImages}}<table><tr><th>{{t "Хранилище"}}</th><th>{{t "Файл"}}</th><th>{{t "Размер, байт"}}</th><th>{{t "Состояние"}}</th><th>{{t "Действия"}}</th></tr>{{range .ISOImages}}<tr><td>{{.StorageName}}</td><td>{{.Image.Path}}</td><td>{{.Image.Size}}</td><td>{{if .Unsupported}}{{t "ARM64: этот профиль поддерживает только x64"}}{{else}}{{if .Image.Present}}{{t "Обнаружен"}}{{else}}{{t "Отсутствует"}}{{end}}{{end}}</td><td>{{if and .CanPrepare (ne $.Info.User.Role "observer")}}<details><summary>{{t "Создать профиль Windows"}}</summary><form method="post" action="/images/windows-profiles"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="storage_id" value="{{.Image.StorageID}}"><input type="hidden" name="path" value="{{.Image.Path}}"><label>{{t "Название в меню"}}</label><input name="title" placeholder="Windows 10 x64" required><label>{{t "Каталог меню"}}</label><input name="folder" value="Windows"><button>{{t "Создать профиль"}}</button></form></details>{{end}}</td></tr>{{end}}</table>{{else}}<p>{{t "ISO пока не найдены."}}</p>{{end}}</section>
{{if .WindowsProfiles}}<section><h2>{{t "Профили Windows"}}</h2><table><tr><th>{{t "Название"}}</th><th>ISO</th><th>{{t "Состояние"}}</th><th>{{t "Действия"}}</th></tr>{{range .WindowsProfiles}}<tr><td>#{{.ID}} {{.Title}}</td><td>{{.Path}}</td><td>{{.State}}{{if .Error}} · {{t .Error}}{{end}}</td><td>{{if ne $.Info.User.Role "observer"}}<form method="post" action="/images/windows-profiles/{{.ID}}/prepare"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Подготовить"}}</button></form>{{end}}{{if eq $.Info.User.Role "admin"}}<details><summary>{{t "Удалить профиль #"}}{{.ID}}</summary><p>{{t "Подготовленные файлы будут удалены. Исходный ISO останется на SMB."}}</p><form method="post" action="/images/windows-profiles/{{.ID}}/delete"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Подтвердить удаление"}}</button></form></details>{{end}}</td></tr>{{end}}</table></section>{{end}}
{{template "answer_forms" .}}
{{end}}{{template "bottom" .}}{{end}}
{{define "answer_forms"}}{{range .WindowsProfiles}}<section><h2>{{t "Сценарии установки для"}} {{.Title}}</h2><p class="muted">{{t "Один ISO может иметь несколько XML-сценариев. Загруженный файл не показывается повторно. Компьютеры в доверенном PXE-сегменте могут получить его содержимое, включая пароли и ключи."}}</p>{{if and (eq .State "ready") (ne $.Info.User.Role "observer")}}<form method="post" enctype="multipart/form-data" action="/images/windows-profiles/{{.ID}}/answers"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><label>{{t "Название пункта меню (латиницей)"}}</label><input name="title" placeholder="Windows 11 Lab" required><label>{{t "Каталог в меню"}}</label><input name="folder" value="{{.Folder}}"><label>{{t "Файл autounattend.xml (до 1 МиБ)"}}</label><input name="file" type="file" accept=".xml,text/xml,application/xml" required><button>{{t "Загрузить сценарий"}}</button></form>{{end}}{{$profileID := .ID}}{{range $.WindowsAnswerVariants}}{{if eq .ProfileID $profileID}}<div class="card"><strong>#{{.ID}} {{.Title}}</strong> · {{.State}}{{if .Error}} · {{t .Error}}{{end}} · <small>SHA-256 {{.SHA256}}</small>{{if eq $.Info.User.Role "admin"}}<details><summary>{{t "Удалить сценарий #"}}{{.ID}}</summary><form method="post" action="/images/windows-answers/{{.ID}}/delete"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Подтвердить удаление"}}</button></form></details>{{end}}</div>{{end}}{{end}}</section>{{end}}{{end}}
{{define "drivers"}}{{template "top" .}}<h1>{{t "Драйверы WinPE"}}</h1><section><p>{{t "Добавьте папку с INF и сопутствующими файлами или ZIP той же папки. Максимум 256 МиБ на передачу. Подготовка и публикация в PXE-меню выполняются отдельно."}}</p>{{if ne .Info.User.Role "observer"}}<div class="grid"><form data-driver-upload="folder" data-csrf="{{.Info.CSRF}}" method="post" enctype="multipart/form-data" action="/drivers/import"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><h2>{{t "Папка драйвера"}}</h2><label>{{t "Папка"}}</label><input type="file" name="files[]" webkitdirectory multiple required><button type="submit">{{t "Загрузить папку"}}</button> <button class="secondary" type="button" data-cancel>{{t "Отменить"}}</button><p><progress max="100" value="0"></progress></p><p role="status"></p></form><form data-driver-upload="zip" data-csrf="{{.Info.CSRF}}" method="post" enctype="multipart/form-data" action="/drivers/import"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><h2>{{t "ZIP-архив"}}</h2><label>ZIP</label><input type="file" name="zip" accept=".zip,application/zip" required><button type="submit">{{t "Загрузить ZIP"}}</button> <button class="secondary" type="button" data-cancel>{{t "Отменить"}}</button><p><progress max="100" value="0"></progress></p><p role="status"></p></form></div><script defer src="/assets/driver-upload.js"></script>{{end}}</section><section><h2>{{t "Каталог пакетов"}}</h2>{{if .DriverPackages}}{{range .DriverPackages}}<div class="card"><strong>{{t "Пакет"}} {{.SHA256}}</strong><p>{{len .Inventory.Files}} {{t "файлов"}}</p><table><tr><th>INF</th><th>{{t "Архитектура"}}</th><th>{{t "Класс"}}</th><th>{{t "Оборудование"}}</th></tr>{{range .Inventory.INFs}}<tr><td>{{.Path}}</td><td>{{.Architecture}}</td><td>{{.Class}}</td><td>{{range .HardwareIDs}}<small>{{.}} </small>{{end}}</td></tr>{{end}}</table></div>{{end}}{{else}}<p>{{t "Пакеты пока не добавлены."}}</p>{{end}}</section>
{{if ne .Info.User.Role "observer"}}<section><h2>{{t "Новая конфигурация установки"}}</h2><p>{{t "Один ISO можно использовать с разными драйверами и XML-сценариями. Отметьте только нужные INF для конкретного оборудования."}}</p><form method="post" action="/drivers/configs"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>Windows ISO</label><select name="profile_id" required>{{range .WindowsProfiles}}{{if eq .State "ready"}}<option value="{{.ID}}">{{.Title}}</option>{{end}}{{end}}</select><label>{{t "XML-сценарий"}}</label><select name="answer_id"><option value="0">{{t "Без XML — ручная установка"}}</option>{{range .WindowsAnswerVariants}}{{if eq .State "ready"}}<option value="{{.ID}}">{{.Title}} (ISO #{{.ProfileID}})</option>{{end}}{{end}}</select><label>{{t "Название пункта PXE-меню (латиницей)"}}</label><input name="title" required placeholder="Windows 11 Laptop A"><label>{{t "Каталог меню"}}</label><input name="folder" value="Windows"><h3>{{t "Драйверы WinPE"}}</h3>{{range .DriverPackages}}{{$hash := .SHA256}}<p><small>{{t "Пакет"}} {{$hash}}</small></p>{{range .Inventory.INFs}}{{if eq .Architecture "amd64"}}<label><input style="width:auto" type="checkbox" name="inf" value="{{$hash}}:{{.Path}}"> {{.Path}} · {{.Class}}</label>{{end}}{{end}}{{end}}<button>{{t "Создать конфигурацию"}}</button></form></section>{{end}}
<section><h2>{{t "Конфигурации установки"}}</h2>{{if .DriverConfigs}}<table><tr><th>{{t "Пункт меню"}}</th><th>{{t "Профиль ISO"}}</th><th>INF</th><th>{{t "Состояние"}}</th><th></th></tr>{{range .DriverConfigs}}<tr><td>#{{.ID}} {{.Title}}<br><small>{{.Folder}}</small></td><td>#{{.ProfileID}}{{if .AnswerID}} · XML #{{.AnswerID}}{{end}}</td><td>{{range .PackageINFs}}<small>{{.INFPath}} · {{.PackageSHA256}}</small><br>{{end}}</td><td>{{.State}}{{if .Error}} · {{t .Error}}{{end}}</td><td>{{if ne $.Info.User.Role "observer"}}<form method="post" action="/drivers/configs/{{.ID}}/prepare"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Подготовить"}}</button></form>{{end}}{{if eq $.Info.User.Role "admin"}}<details><summary>{{t "Удалить конфигурацию #"}}{{.ID}}</summary><p>{{t "Её подготовленные файлы будут удалены; общий WIM и драйверы останутся, если используются другими пунктами."}}</p><form method="post" action="/drivers/configs/{{.ID}}/delete"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">{{t "Подтвердить удаление"}}</button></form></details>{{end}}</td></tr>{{end}}</table>{{else}}<p>{{t "Конфигураций пока нет."}}</p>{{end}}</section>
{{template "bottom" .}}{{end}}
`

func render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var out bytes.Buffer
	lang := normalizeLanguage(data.Lang)
	if data.Lang == "" {
		lang = normalizeLanguage(data.Info.User.Language)
	}
	for _, field := range []*string{&data.Message, &data.Error, &data.ImageError, &data.EventError, &data.BootStatus, &data.AgentStatus} {
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
