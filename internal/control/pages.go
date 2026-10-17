package control

import (
	"bytes"
	"html/template"
	"net/http"

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

type pageData struct {
	Info                  requestContext
	CSRF                  string
	Message               string
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
	Versions              []MenuVersion
	MenuTitle             string
	MenuLabel             string
	MenuMessage           string
	MenuUbuntu            bool
	MenuWindows10         bool
	MenuWindows11         bool
	MenuFolderUbuntu      string
	MenuFolderWindows10   string
	MenuFolderWindows11   string
	HasMenuFolders        bool
	ImageError            string
	Storages              []images.Storage
	ISOImages             []imageRow
	WindowsProfiles       []WindowsProfile
	WindowsAnswerVariants []WindowsAnswerVariant
	MenuWindowsProfiles   []menuWindowsProfileRow
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

var pages = template.Must(template.New("pages").Funcs(template.FuncMap{"storageKind": storageKindLabel, "storageState": storageStateLabel}).Parse(`
{{define "top"}}<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>PXE Platform</title><style>
:root{font-family:system-ui,-apple-system,sans-serif;color:#172437;background:#f3f6fa}*{box-sizing:border-box}body{margin:0}header{background:#122640;color:white;padding:16px 24px;display:flex;align-items:center;gap:24px;flex-wrap:wrap}header strong{font-size:1.2rem}nav{display:flex;gap:14px;flex-wrap:wrap}a{color:#1868aa}header a{color:#d4e7fa;text-decoration:none}header span{margin-left:auto}main{max-width:1160px;margin:28px auto;padding:0 20px}h1{font-size:1.65rem;margin:0 0 20px}h2{font-size:1.2rem}section,.card{background:white;border:1px solid #dce4ed;border-radius:12px;padding:20px;margin:0 0 20px;box-shadow:0 2px 9px #14283a09}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px}.grid .card{margin:0}label{display:block;font-weight:600;margin:12px 0 5px}input,select{width:100%;max-width:520px;padding:10px;border:1px solid #a9b9c9;border-radius:7px;font:inherit}button{font:inherit;background:#0b6db8;color:white;border:0;border-radius:7px;padding:10px 16px;cursor:pointer;margin-top:12px}button.secondary{background:#495e75}table{border-collapse:collapse;width:100%;font-size:.92rem}td,th{text-align:left;padding:9px;border-bottom:1px solid #e4eaf0;vertical-align:top}th{color:#516277}small,.muted{color:#596e83}.error{color:#a52d2d}.notice{padding:12px;border-radius:8px;background:#eef6ff;margin-bottom:18px}.inline{display:inline}.inline button{margin:0;padding:5px 9px}pre{overflow:auto;background:#eef3f7;padding:14px;border-radius:7px}
</style></head><body><header><strong>PXE Platform</strong>{{if .Info.User.ID}}<nav><a href="/">Обзор</a><a href="/events">Журнал</a><a href="/network">Сеть</a><a href="/menu">Меню</a><a href="/images">Образы</a>{{if eq .Info.User.Role "admin"}}<a href="/users">Пользователи</a><a href="/audit">Аудит</a>{{end}}</nav><span>{{.Info.User.Username}} · {{.Info.User.Role}}</span><form class="inline" method="post" action="/logout"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><button class="secondary">Выйти</button></form>{{end}}</header><main>{{if .Message}}<div class="notice">{{.Message}}</div>{{end}}{{end}}
{{define "bottom"}}</main></body></html>{{end}}
{{define "login"}}{{template "top" .}}<section style="max-width:430px;margin:80px auto"><h1>Вход в админку</h1><form method="post" action="/login"><input type="hidden" name="csrf" value="{{.CSRF}}"><label>Имя пользователя</label><input name="username" autocomplete="username" required><label>Пароль</label><input type="password" name="password" autocomplete="current-password" required><button>Войти</button></form></section>{{template "bottom" .}}{{end}}
{{define "dashboard"}}{{template "top" .}}<h1>Обзор</h1><div class="grid"><section class="card"><h2>Boot</h2><p>{{.BootStatus}}</p></section><section class="card"><h2>Network Agent</h2><p>{{.AgentStatus}}</p></section></div><section><h2>Последние запросы</h2>{{if .EventError}}<p class="error">{{.EventError}}</p>{{else}}<table><tr><th>Время</th><th>Тип</th><th>Путь</th><th>Клиент</th></tr>{{range .Events}}<tr><td>{{.At.Format "02.01 15:04:05"}}</td><td>{{.Kind}}</td><td>{{.Path}}</td><td>{{.RemoteAddr}}</td></tr>{{end}}</table>{{end}}</section>{{template "bottom" .}}{{end}}
{{define "events"}}{{template "top" .}}<h1>Журнал запросов клиентов</h1><section><p class="muted">Здесь показаны подтверждённые HTTP-запросы меню и файлов.</p>{{if .EventError}}<p class="error">{{.EventError}}</p>{{else}}<table><tr><th>Время</th><th>Тип</th><th>Путь</th><th>Клиент</th></tr>{{range .Events}}<tr><td>{{.At.Format "02.01.2006 15:04:05"}}</td><td>{{.Kind}}</td><td>{{.Path}}</td><td>{{.RemoteAddr}}</td></tr>{{end}}</table>{{end}}</section>{{template "bottom" .}}{{end}}
{{define "network"}}{{template "top" .}}<h1>PXE-сеть</h1><section>{{if .HasNetwork}}<p>Активный режим: <strong>{{.Network.Mode}}</strong> · интерфейс <strong>{{.Network.Interface}}</strong> · адрес {{.Network.ServerIP}}</p>{{else}}<p class="error">Активная конфигурация недоступна.</p>{{end}}</section>{{if eq .Info.User.Role "admin"}}<section><h2>Изменить конфигурацию</h2><form method="post" action="/network/preview"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>Режим</label><select name="mode"><option value="proxy" {{if eq .Network.Mode "proxy"}}selected{{end}}>Proxy DHCP — адреса выдаёт роутер</option><option value="full" {{if eq .Network.Mode "full"}}selected{{end}}>Полный DHCP</option></select><label>Интерфейс PXE</label><input name="interface" value="{{.Network.Interface}}" required><label>IP сервера</label><input name="server_ip" value="{{.Network.ServerIP}}" required><label>Подсеть CIDR</label><input name="subnet" value="{{.Network.Subnet}}" required><label>Начало DHCP-пула</label><input name="dhcp_start" value="{{.Network.DHCPStart}}"><label>Конец DHCP-пула</label><input name="dhcp_end" value="{{.Network.DHCPEnd}}"><label>URL загрузочного меню</label><input name="boot_url" value="{{.Network.BootURL}}" required><label>Каталог TFTP</label><input name="tftp_root" value="{{.Network.TFTPRoot}}" required><button>Проверить изменения</button></form></section>{{end}}{{template "bottom" .}}{{end}}
{{define "network_preview"}}{{template "top" .}}<h1>Подтверждение сети</h1><section><p>Проверьте настройки перед применением. Это может изменить выдачу PXE в выбранном сегменте.</p><table><tr><th></th><th>Сейчас</th><th>Будет</th></tr><tr><td>Режим</td><td>{{.OldNetwork.Mode}}</td><td>{{.Network.Mode}}</td></tr><tr><td>Интерфейс</td><td>{{.OldNetwork.Interface}}</td><td>{{.Network.Interface}}</td></tr><tr><td>Адрес</td><td>{{.OldNetwork.ServerIP}}</td><td>{{.Network.ServerIP}}</td></tr><tr><td>Подсеть</td><td>{{.OldNetwork.Subnet}}</td><td>{{.Network.Subnet}}</td></tr><tr><td>Пул</td><td>{{.OldNetwork.DHCPStart}}–{{.OldNetwork.DHCPEnd}}</td><td>{{.Network.DHCPStart}}–{{.Network.DHCPEnd}}</td></tr></table><form method="post" action="/network/apply"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><input type="hidden" name="confirmation" value="{{.Confirmation}}"><input type="hidden" name="mode" value="{{.Network.Mode}}"><input type="hidden" name="interface" value="{{.Network.Interface}}"><input type="hidden" name="server_ip" value="{{.Network.ServerIP}}"><input type="hidden" name="subnet" value="{{.Network.Subnet}}"><input type="hidden" name="dhcp_start" value="{{.Network.DHCPStart}}"><input type="hidden" name="dhcp_end" value="{{.Network.DHCPEnd}}"><input type="hidden" name="boot_url" value="{{.Network.BootURL}}"><input type="hidden" name="tftp_root" value="{{.Network.TFTPRoot}}"><button>Применить</button> <a href="/network">Отмена</a></form></section>{{template "bottom" .}}{{end}}
{{define "menu"}}{{template "top" .}}<h1>Загрузочное меню</h1>{{if ne .Info.User.Role "observer"}}<section><h2>Новая версия</h2><form method="post" action="/menu/publish"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>Заголовок</label><input name="title" value="{{.MenuTitle}}" required><label>Диагностический пункт</label><input name="label" value="{{.MenuLabel}}" required><label>Сообщение диагностики</label><input name="message" value="{{.MenuMessage}}" required><label><input style="width:auto" type="checkbox" name="ubuntu" {{if .MenuUbuntu}}checked{{end}}> Ubuntu Desktop 26.04.1 (тестовая загрузка, ISO 6,5 ГБ)</label><label><input style="width:auto" type="checkbox" name="windows10" {{if .MenuWindows10}}checked{{end}}> Windows 10 x64 — запуск WinPE (источник установки по SMB)</label><label><input style="width:auto" type="checkbox" name="windows11" {{if .MenuWindows11}}checked{{end}}> Windows 11 x64 — запуск WinPE (проверка)</label>{{range .MenuWindowsProfiles}}<label><input style="width:auto" type="checkbox" name="winiso_{{.ID}}" {{if .Selected}}checked{{end}}> {{.Title}} · {{.State}}</label><label>Каталог для {{.Title}}</label><input name="folder_winiso_{{.ID}}" value="{{.MenuFolder}}" placeholder="Windows">{{end}}<details {{if .HasMenuFolders}}open{{end}}><summary>Каталоги в PXE-меню</summary><p class="muted">Укажите путь, например Windows/Установщики. Оставьте поле пустым для главного меню.</p><label>Ubuntu Desktop</label><input name="folder_ubuntu" value="{{.MenuFolderUbuntu}}" placeholder="Linux"><label>Windows 10</label><input name="folder_windows10" value="{{.MenuFolderWindows10}}" placeholder="Windows"><label>Windows 11</label><input name="folder_windows11" value="{{.MenuFolderWindows11}}" placeholder="Windows"></details><button>Опубликовать</button></form></section>{{end}}<section><h2>Версии</h2><table><tr><th>Время</th><th>Название</th><th>Автор</th><th>SHA-256</th><th>Состояние</th><th></th></tr>{{range .Versions}}<tr><td>{{.At.Format "02.01.2006 15:04"}}</td><td>{{.Title}}</td><td>{{.Author}}</td><td><small>{{.SHA256}}</small></td><td>{{if .Active}}Активна{{end}}</td><td>{{if and (not .Active) (ne $.Info.User.Role "observer")}}<form method="post" action="/menu/rollback"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="secondary">Вернуть</button></form>{{end}}</td></tr>{{end}}</table></section>{{template "bottom" .}}{{end}}
{{define "users"}}{{template "top" .}}<h1>Пользователи</h1><section><h2>Добавить</h2><form method="post" action="/users/create"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>Имя</label><input name="username" required><label>Пароль, от 12 символов</label><input type="password" name="password" required><label>Роль</label><select name="role"><option value="observer">Наблюдатель</option><option value="operator">Оператор</option><option value="admin">Администратор</option></select><button>Создать</button></form></section><section><h2>Список</h2><table><tr><th>Имя</th><th>Роль и доступ</th><th></th></tr>{{range .Users}}<tr><td>{{.Username}} <small>#{{.ID}}</small></td><td><form method="post" action="/users/change"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><select name="role"><option value="observer" {{if eq .Role "observer"}}selected{{end}}>Наблюдатель</option><option value="operator" {{if eq .Role "operator"}}selected{{end}}>Оператор</option><option value="admin" {{if eq .Role "admin"}}selected{{end}}>Администратор</option></select><label><input style="width:auto" type="checkbox" name="enabled" {{if .Enabled}}checked{{end}}> Активен</label><button>Сохранить</button></form></td></tr>{{end}}</table></section><section><h2>Сменить пароль</h2><form method="post" action="/users/reset-password"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>ID пользователя из списка</label><input name="id" inputmode="numeric" required><label>Новый пароль, от 12 символов</label><input name="password" type="password" required><button>Сменить пароль и завершить его сеансы</button></form></section>{{template "bottom" .}}{{end}}
{{define "audit"}}{{template "top" .}}<h1>Аудит изменений</h1><section><table><tr><th>Время</th><th>Автор</th><th>Действие</th><th>Результат</th><th>Детали</th></tr>{{range .Audit}}<tr><td>{{.At.Format "02.01.2006 15:04:05"}}</td><td>{{.Actor}}</td><td>{{.Action}}</td><td>{{.Result}}</td><td>{{.Detail}}</td></tr>{{end}}</table></section>{{template "bottom" .}}{{end}}
{{define "images"}}{{template "top" .}}<h1>Образы ISO</h1>
{{if eq .Info.User.Role "admin"}}<section><h2>Добавить сетевую папку SMB</h2><p class="muted">Подключение только для чтения. Укажите прямой путь к папке с ISO.</p><form method="post" action="/images/smb"><input type="hidden" name="csrf" value="{{.Info.CSRF}}"><label>Путь к ISO</label><input name="path" placeholder="//192.168.10.10/Downloads/iso" required><label>Пользователь</label><input name="username" required autocomplete="off"><label>Пароль</label><input name="password" type="password" required autocomplete="new-password"><details><summary>Домен, если нужен</summary><input name="domain" autocomplete="off"></details><button>Подключить и найти ISO</button></form></section>{{end}}
{{if .ImageError}}<section><p class="error">{{.ImageError}}</p></section>{{else}}
<section><h2>Хранилища</h2>{{if .Storages}}<table><tr><th>Название</th><th>Тип</th><th>Состояние</th><th>Расположение</th><th>Действия</th></tr>{{range .Storages}}<tr><td>{{.Name}}</td><td>{{storageKind .Kind}}</td><td>{{storageState .State}}{{if .Error}} · {{.Error}}{{end}}</td><td>{{if eq .Kind "smb"}}//{{.Server}}/{{.Share}}{{if .Subpath}}/{{.Subpath}}{{end}}{{else}}{{.Root}}{{end}}</td><td>{{if ne $.Info.User.Role "observer"}}<form method="post" action="/images/scan"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="id" value="{{.ID}}"><button class="secondary">Обновить</button></form>{{end}}{{if and (eq $.Info.User.Role "admin") (eq .Kind "smb")}}<details><summary>Повторить подключение</summary><form method="post" action="/images/smb/{{.ID}}/retry"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><label>Пользователь</label><input name="username" required autocomplete="off"><label>Пароль</label><input name="password" type="password" required autocomplete="new-password"><label>Домен, если нужен</label><input name="domain" autocomplete="off"><button>Подключить</button></form></details><form method="post" action="/images/smb/{{.ID}}/remove"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">Удалить</button></form>{{end}}</td></tr>{{end}}</table>{{else}}<p>Хранилища пока не добавлены.</p>{{end}}</section>
<section><h2>Найденные ISO</h2><p class="muted">Для Windows x64 из SMB-хранилища можно создать отдельный загрузочный профиль. Название и каталог в PXE-меню указывайте латиницей; формат ISO проверяется при подготовке.</p>{{if .ISOImages}}<table><tr><th>Хранилище</th><th>Файл</th><th>Размер, байт</th><th>Состояние</th><th>Действия</th></tr>{{range .ISOImages}}<tr><td>{{.StorageName}}</td><td>{{.Image.Path}}</td><td>{{.Image.Size}}</td><td>{{if .Unsupported}}ARM64: этот профиль поддерживает только x64{{else}}{{if .Image.Present}}Обнаружен{{else}}Отсутствует{{end}}{{end}}</td><td>{{if and .CanPrepare (ne $.Info.User.Role "observer")}}<details><summary>Создать профиль Windows</summary><form method="post" action="/images/windows-profiles"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><input type="hidden" name="storage_id" value="{{.Image.StorageID}}"><input type="hidden" name="path" value="{{.Image.Path}}"><label>Название в меню</label><input name="title" placeholder="Windows 10 x64" required><label>Каталог меню</label><input name="folder" value="Windows"><button>Создать профиль</button></form></details>{{end}}</td></tr>{{end}}</table>{{else}}<p>ISO пока не найдены.</p>{{end}}</section>
{{if .WindowsProfiles}}<section><h2>Профили Windows</h2><table><tr><th>Название</th><th>ISO</th><th>Состояние</th><th>Действия</th></tr>{{range .WindowsProfiles}}<tr><td>{{.Title}}</td><td>{{.Path}}</td><td>{{.State}}{{if .Error}} · {{.Error}}{{end}}</td><td>{{if ne $.Info.User.Role "observer"}}<form method="post" action="/images/windows-profiles/{{.ID}}/prepare"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><button class="secondary">Подготовить</button></form>{{end}}</td></tr>{{end}}</table></section>{{end}}
{{template "answer_forms" .}}
{{end}}{{template "bottom" .}}{{end}}
{{define "answer_forms"}}{{range .WindowsProfiles}}{{if eq .State "ready"}}<section><h2>Сценарии установки для {{.Title}}</h2><p class="muted">Один ISO может иметь несколько XML-сценариев. Загруженный файл не показывается повторно. Компьютеры в доверенном PXE-сегменте могут получить его содержимое, включая пароли и ключи.</p>{{if ne $.Info.User.Role "observer"}}<form method="post" enctype="multipart/form-data" action="/images/windows-profiles/{{.ID}}/answers"><input type="hidden" name="csrf" value="{{$.Info.CSRF}}"><label>Название пункта меню (латиницей)</label><input name="title" placeholder="Windows 11 Lab" required><label>Каталог в меню</label><input name="folder" value="{{.Folder}}"><label>Файл autounattend.xml (до 1 МиБ)</label><input name="file" type="file" accept=".xml,text/xml,application/xml" required><button>Загрузить сценарий</button></form>{{end}}{{$profileID := .ID}}{{range $.WindowsAnswerVariants}}{{if eq .ProfileID $profileID}}<p><strong>{{.Title}}</strong> · {{.State}}{{if .Error}} · {{.Error}}{{end}} · <small>SHA-256 {{.SHA256}}</small></p>{{end}}{{end}}</section>{{end}}{{end}}{{end}}
`))

func render(w http.ResponseWriter, name string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var out bytes.Buffer
	if err := pages.ExecuteTemplate(&out, name, data); err != nil {
		http.Error(w, "Ошибка отображения страницы", 500)
		return
	}
	_, _ = w.Write(out.Bytes())
}
