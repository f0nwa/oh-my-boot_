# Архитектура Oh My Boot (OMB)

Документ описывает устройство служб Oh My Boot, разделение данных между базами PostgreSQL, слои сущностей от физических хранилищ до PXE-клиентов и сквозной конвейер сетевой загрузки.

## 1. Службы, сокеты и границы доступа

OMB построен как набор изолированных служб с минимальными привилегиями. Внешние сетевые порты открыты только у служб, которые непосредственно общаются с администратором или загружаемыми машинами; весь внутренний обмен между компонентами идёт по локальным Unix-сокетам с разграничением прав через системные группы.

```mermaid
flowchart LR
    Admin["Администратор (HTTPS :8443 / SFTP :2022)"]
    Client["PXE-клиент (BIOS / UEFI Secure Boot)"]

    subgraph Core ["Службы OMB"]
        Control["omb-control\n(Веб-интерфейс, SFTP :2022)"]
        Boot["omb-boot\n(HTTP :8080, меню iPXE)"]
        Images["omb-images\n(Каталог ISO и сканирование)"]
        Agent["omb-agent (root)\n(Сеть, монтирование, WinPE, NTP)"]
        Dnsmasq["omb-dnsmasq\n(DHCP / Proxy DHCP :67, TFTP :69)"]
        WinISO["omb-winiso@<id>\n(Loopback-монтирование ISO)"]
    end

    subgraph Infra ["Инфраструктура ОС"]
        PG["PostgreSQL\nomb_control · omb_images · omb_boot"]
        Samba["Samba (smbd)\n[ombisos] RO · [iso] приём"]
        TFTP["/srv/omb/tftp\n(загрузчики, WIM, профили)"]
    end

    Admin -->|HTTPS :8443| Control
    Admin -.->|SFTP :2022| Control
    Admin -.->|SMB [iso]| Samba

    Control -->|/run/omb-boot/control.sock| Boot
    Control -->|/run/omb-images/images.sock| Images
    Control -->|/run/omb/agent.sock| Agent

    Control --> PG
    Images --> PG
    Boot --> PG

    Agent -->|конфиг + SIGHUP| Dnsmasq
    Agent -->|запуск юнита| WinISO
    Agent -->|запись артефактов| TFTP
    Agent -->|конфиг шары| Samba
    WinISO -->|mount -o loop,ro| Samba
    Boot -->|чтение файлов| TFTP

    Client -->|DHCP/Proxy + TFTP| Dnsmasq
    Client -->|HTTP :8080| Boot
    Client -->|SMB [ombisos] RO| Samba
```

### Роли служб

| Служба / юнит | Пользователь | Назначение |
| --- | --- | --- |
| `omb-control.service` | `omb-control` | Веб-панель администрирования (HTTPS), управление пользователями, ролями, 2FA, внешним входом (AD/LDAP, Keycloak OIDC), черновиком меню, загрузками, встроенный SFTP-сервер приёма ISO (`:2022`). |
| `omb-boot.service` | `omb-boot` | Отдача опубликованного меню (`/boot/menu.ipxe`), загрузочных файлов (`/assets/...`), файлов Ubuntu Autoinstall (`/autoinstall/...`), приём телеметрии (`/boot/hello`, `/boot/choice`), режим обслуживания на лету и журнал загрузок. |
| `omb-images.service` | `omb-images` | Инвентаризация локального, SMB- и NFS-хранилищ, инспекция ISO (`wimlib-imagex`), потоковая загрузка и миграция образов между хранилищами, фоновый рескан и учёт занятого места. |
| `omb-agent.service` | `root` | Единственная привилегированная служба: настройка `omb-dnsmasq` и правил драйверов по MAC/OUI, монтирование SMB/NFS и loopback ISO (`omb-winiso@`), сборка WinPE `boot.wim` с драйверами, настройка Samba и системного времени/NTP. |
| `omb-dnsmasq.service` | `dnsmasq` | Выделенный экземпляр `dnsmasq` с конфигурацией `/etc/omb/dnsmasq.conf`: полный DHCP или Proxy DHCP (порт 67/4011) и TFTP-раздача начальных загрузчиков (порт 69). |
| `omb-winiso@.service` | `root` | Шаблонный oneshot-юнит (`/usr/local/bin/omb-winiso-prepare %i`), который монтирует конкретный Windows/Strelec ISO в `/mnt/omb-iso/profiles/<id>` в режиме только чтения для раздачи по Samba. |

### Три изолированные базы PostgreSQL

- **`omb_control`** — учётные записи, сессии, политика паролей, TOTP 2FA и доверенные браузеры, интеграции AD/LDAP и Keycloak, аудит, системные события, машины и правила по префиксу MAC, загрузки (`installations`), черновик и версии меню, профили Windows/Ubuntu, файлы ответов и ручные проверки, пакеты и конфигурации драйверов WinPE, блокировки обслуживания (`maintenance_locks`), миграции ISO и настройки файлового сервера.
- **`omb_images`** — подключённые хранилища (`storages`), каталог обнаруженных образов (`images`) и расписание фоновых задач (`catalog_settings`).
- **`omb_boot`** — журнал HTTP-запросов и событий загрузки PXE-клиентов (`boot_events`).

Секреты (пароли интеграций, секреты OIDC, ключи TOTP) хранятся в `omb_control` в зашифрованном виде (AES-256-GCM) с ключом `/etc/omb/secrets.key`.

---

## 2. Слои сущностей и связей

Сущности OMB разделены на шесть слоёв. Это позволяет:
- использовать один и тот же файл ответов для разных ISO (скрипты не привязаны жёстко к образу);
- переиспользовать собранные конфигурации драйверов WinPE по контрольной сумме SHA-256;
- перемещать исходный ISO между локальным диском, SMB и NFS без поломки загрузок и опубликованного меню;
- переводить хранилище или образ в режим обслуживания без перепубликации черновика меню.

```mermaid
flowchart TD
    subgraph L1 ["1. Клиенты и сетевые правила"]
        Client["ПК / ВМ (MAC, BIOS / UEFI)"]
        Rules["Машины и правила OUI\n(iPXE vs SNP, DHCP ignore)"]
        Client -->|DHCP / Proxy DHCP| Rules
    end

    subgraph L2 ["2. Меню и Загрузки"]
        Menu["Опубликованное меню\n(/var/lib/omb-boot/menu.ipxe)"]
        Maint["Режим обслуживания\n(maintenance_locks → HTTP 503 / пауза)"]
        Inst["Загрузка (Installation)\n— рецепт развёртывания"]
        Rules -->|TFTP загрузчик → HTTP /boot/menu.ipxe| Menu
        Maint -.->|динамический фильтр| Menu
        Menu -->|выбор пункта| Inst
    end

    subgraph L3 ["3. Скрипты установки и WinPE"]
        Answers["Скрипты установки\n(autounattend.xml / Ubuntu user-data)"]
        WinPE["Конфигурации WinPE\n(boot.wim + INF + omb-setup.cmd)"]
        Inst -.->|опционально| Answers
        Inst -.->|Windows / Strelec| WinPE
    end

    subgraph L4 ["4. Профили и образы ISO"]
        WinISO["Профиль Windows ISO\n(install.wim / boot.wim)"]
        UbuSrc["Источник Ubuntu\n(vmlinuz / initrd / ISO)"]
        Strelec["Сервисный образ\n(Strelec / WinPE)"]
        Inst ===> WinISO
        Inst ===> UbuSrc
        Inst ===> Strelec
    end

    subgraph L5 ["5. Runtime-раздача клиентам"]
        SambaRO["Samba [ombisos] (RO)\n/mnt/omb-iso/profiles/<id>"]
        BootHTTP["omb-boot HTTP :8080\n/assets/ · /autoinstall/"]
        WinISO -->|omb-winiso@id| SambaRO
        Strelec -->|omb-winiso@id| SambaRO
        UbuSrc -->|omb-agent| BootHTTP
        WinPE -->|кэш /srv/omb/tftp| BootHTTP
    end

    subgraph L6 ["6. Физические хранилища"]
        Local["Локальный диск\n/var/lib/omb-images/iso"]
        SMB["SMB-хранилище\n/mnt/omb-smb"]
        NFS["NFS-хранилище (v4.1 / v3)\n/mnt/omb-nfs"]
        WinISO --- SMB
        UbuSrc --- NFS
        Strelec --- Local
    end

    Client -.->|WinPE: net use I:| SambaRO
    Client -.->|iPXE / cloud-init| BootHTTP
```

### Кратко по слоям

1. **Клиенты (`machines`, `mac_prefix_rules`)** — каждая загрузившаяся машина фиксируется по MAC-адресу. Для глобального уровня, префикса MAC (3–5 байт) или конкретной машины задаётся драйвер UEFI (`ipxe.efi` или `snponly.efi`). При DHCP-флуде проблемный MAC можно изолировать правилом `<mac>,ignore` в `omb-dnsmasq` через `SIGHUP` без перезапуска службы.
2. **Меню и Загрузки (`installations`, `menu_draft`, `menu_versions`)** — «Загрузка» объединяет образ ОС, папку и название в меню, опциональный скрипт установки и конфигурацию драйверов WinPE. Изменения накапливаются в черновике (`menu_draft`) и не видны клиентам до явной публикации. Каждая опубликованная версия хранит снимок своих компонентов и допускает откат.
3. **Скрипты установки (`windows_answer_variants`, `ubuntu_autoinstall_files`, `answer_installation_checks`)** — независимая библиотека сценариев Windows XML и Ubuntu cloud-init. Для Windows-сценариев совместимость с конкретным образом проверяется по метаданным XML и `install.wim`; в системе также хранится история ручных проверок (`answer_installation_checks`).
4. **Драйверы и конфигурации WinPE (`winpe_driver_packages`, `winpe_driver_configs`)** — загруженные пакеты драйверов (папка или ZIP) внедряются через `wimlib-imagex` в базовый `boot.wim` вместе со скриптом `omb-setup.cmd` (параметры ожидания сети и действия по завершении Setup). Готовые WIM-файлы дедуплицируются по SHA-256 и хранятся в `/srv/omb/tftp/windows-driver-wims/`.
5. **Образы ISO и runtime-монтирования (`omb_images.images`, `windows_profiles`, `ubuntu_sources`, `image_migrations`)** — `omb-images` индексирует ISO и собирает метаданные. Для Windows и Strelec `omb-agent` поднимает loopback-монтирование `omb-winiso@<id>` в `/mnt/omb-iso/profiles/<id>` и публикует его через read-only шару Samba `[ombisos]`. Для Ubuntu ядро и `initrd` извлекаются в `/srv/omb/tftp/ubuntu-sources/`. При перемещении ISO между хранилищами выполняется потоковый перенос с атомарной перепривязкой профилей и монтирований.
6. **Хранилища и приём файлов (`omb_images.storages`, `file_server_settings`)** — локальный каталог `/var/lib/omb-images/iso`, внешние SMB-шары и NFS-экспорты (NFSv4.1/v3 с автоопределением `ro`/`rw`). Для удобной загрузки ISO в локальное хранилище предусмотрен отключаемый локальный файловый сервер: управляемая шара Samba `[iso]` (строго изолированная от PXE-шары `[ombisos]`) и встроенный SFTP-сервер на порту `2022` с отдельным паролем доступа в профиле пользователя.

---

## 3. Сквозной конвейер сетевой загрузки

### Windows (BIOS и UEFI с Secure Boot)

1. Клиент отправляет `DHCP DISCOVER`; `omb-dnsmasq` выдаёт адрес (или только PXE-опции в режиме Proxy DHCP) и имя начального загрузчика:
   - **UEFI (Secure Boot):** `ipxe-shim.efi` (или `snponly-shim.efi`, обе ссылки ведут на двойно подписанный Microsoft UEFI CA 2011/2023 `shimx64.efi` из iPXE shim 16.1), который запускает подписанный `ipxe.efi` или `snponly.efi`.
   - **Legacy BIOS:** `ipxeboot/x86_64/undionly.kpxe`.
2. iPXE запрашивает `http://<OMB-IP>:8080/boot/menu.ipxe` и отправляет идентификационные параметры (`/boot/hello`).
3. Оператор выбирает пункт меню (или истекает таймер пункта по умолчанию); `omb-boot` фиксирует выбор (`/boot/choice`). Если образ или хранилище в режиме обслуживания, клиент получает экран паузы без запуска установки.
4. iPXE скачивает по HTTP `wimboot`, `BCD`, `boot.sdi`, модифицированный `boot.wim` (с внедрёнными драйверами) и `omb-setup.cmd` (выдача `windows-driver-configs/*/omb-setup.cmd` ограничена загрузочной подсетью).
5. В среде WinPE запускается `omb-setup.cmd`: ожидает сеть с заданными таймаутом и паузой, подключает read-only шару `\\<OMB-IP>\ombisos\<token>` как диск `I:`, при наличии файла ответов копирует его в `X:\autounattend.xml` и запускает `I:\sources\setup.exe`.
6. По завершении Windows Setup выполняется настроенное действие (перезагрузка, выключение или командная строка).

### Ubuntu Desktop

1. Клиент проходит ту же цепочку DHCP/TFTP → iPXE → `/boot/menu.ipxe`.
2. При выборе пункта Ubuntu iPXE загружает по HTTP ядро `vmlinuz` и `initrd` из `/assets/ubuntu-sources/...`.
3. В параметрах ядра передаются URL образа ISO (HTTP) и, если выбран скрипт установки, источник `autoinstall ds=nocloud-net;s=http://<OMB-IP>:8080/autoinstall/.../`.

### Сервисный образ Strelec

1. Используется нативный PXE-сценарий образа: iPXE загружает `wimboot` и подготовленный WinPE-образ по HTTP.
2. После старта WinPE подключает read-only шару `[ombisos]` для доступа к набору утилит и программ из исходного ISO.

---

## 4. Карта основных путей и сокетов на сервере

| Путь | Владелец / служба | Назначение |
| --- | --- | --- |
| `/etc/omb/` | `omb-agent`, службы | Конфигурации `dnsmasq.conf`, `*.env`, ключ шифрования `secrets.key`, шаблон `windows-profiles/omb-setup.cmd.in`. |
| `/run/omb/agent.sock` | `omb-agent` | Локальный Unix-сокет управления сетью, монтированиями и сборкой WinPE. |
| `/run/omb-boot/control.sock` | `omb-boot` | Локальный Unix-сокет публикации меню, режима обслуживания, журнала и отмены HTTP-передач. |
| `/run/omb-images/images.sock` | `omb-images` | Локальный Unix-сокет каталога хранилищ, сканирования, загрузки и миграции ISO. |
| `/var/lib/omb-images/iso` | `omb-images` | Локальное хранилище исходных ISO-образов (и корень локального SMB `[iso]` / SFTP). |
| `/var/lib/omb-boot/menu.ipxe` | `omb-boot` | Опубликованный снимок активного меню iPXE. |
| `/var/lib/omb-control/` | `omb-control` | TLS-сертификат и ключ веб-интерфейса (`control.crt`, `control.key`). |
| `/srv/omb/tftp/` | `omb-dnsmasq`, `omb-boot`, `omb-agent` | Загрузчики iPXE/shim (`ipxeboot/`), профили Windows, кэш драйверных `boot.wim`, скрипты `omb-setup.cmd`, источники и `autoinstall` Ubuntu. |
| `/mnt/omb-smb`, `/mnt/omb-nfs` | `omb-agent`, `omb-images` | Точки монтирования подключённых сетевых хранилищ (и `*-upload` для записи). |
| `/mnt/omb-iso/profiles/<id>` | `omb-winiso@` | Read-only loopback-монтирования Windows и Strelec ISO для Samba `[ombisos]`. |

---

## Связанные документы

- [Установка и настройка Control](control-installation.md)
- [Каталог образов ISO и профили Windows](images-installation.md)
- [Цепочка Secure Boot](secure-boot.md) и [пути сетевой загрузки (iPXE, GRUB, Windows)](secure-boot-boot-paths.md)
- [Переносимый офлайн-выпуск и обновление](offline-release.md)
- [Восстановление сервера](recovery.md)
