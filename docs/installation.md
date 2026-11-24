# Установка oh my boot_ (OMB)

[English version](en/installation.md)

Это единое руководство по первичной установке OMB на сервер Ubuntu без контейнеров. После установки вы получите готовую HTTPS-панель управления (`https://<ip>:8443`), службы сетевой загрузки (BIOS/UEFI и UEFI Secure Boot), каталог ISO-образов и — при выборе PXE-интерфейса в мастере — сразу настроенные DHCP (`omb-dnsmasq`) и Samba (`smbd`).

## Требования к серверу

- **ОС и архитектура:**
  - **Ubuntu 26.04 LTS amd64** — основная целевая ОС (для онлайн- и офлайн-установки).
  - **Ubuntu 24.04 LTS amd64** — поддерживается для онлайн-установки (для других версий в `install.sh` предусмотрен флаг `--force-os`).
- **Ресурсы машины:**
  - **Минимум** *(если ISO хранятся на внешнем SMB/NFS)*: 2 vCPU (`x86_64`), 2 ГБ ОЗУ, 20 ГБ диска, 1 Гбит/с.
  - **Рекомендуется** *(локальное хранение ISO и одновременные установки)*: 4+ vCPU (ускоряет сборку `boot.wim` с драйверами), 4–8 ГБ ОЗУ (PostgreSQL + дисковый кэш), 60–100+ ГБ SSD.
- **Сеть:** 1 или 2 сетевых интерфейса со **статическим IPv4-адресом**:
  - **2 интерфейса (рекомендуется):** один — для управления и доступа к веб-панели, второй — выделенный для PXE-сегмента (режим **Full DHCP** или **Proxy DHCP**).
  - **1 интерфейс:** веб-панель и PXE работают на одном интерфейсе; в общей сети используйте **Proxy DHCP**, чтобы не конфликтовать с существующим DHCP-сервером.
- **Права:** запуск установки от `root` (`sudo`).

## Сетевые порты и файрвол

На чистой установке Ubuntu Server файрвол UFW по умолчанию выключен — все нужные порты открываются автоматически при запуске служб OMB. Если в вашей инфраструктуре включён UFW или внешний межсетевой экран, разрешите порты из таблицы ниже. Перед установкой `omb-setup` проверяет, что обязательные порты не заняты другими службами, а после установки (и в `omb status`) предупреждает, если UFW активен.

| Сегмент | Порт | Протокол | Назначение | Обязательность |
| --- | --- | --- | --- | --- |
| **Management** | `8443` | TCP | Веб-панель управления (`omb-control`, HTTPS) | Обязательный |
| **Management** | `2022` | TCP | Встроенный SFTP-сервер для загрузки ISO | Опциональный (если включён) |
| **Management** | `445` | TCP | Samba-шара `[iso]` для загрузки образов с рабочих станций | Опциональный (если включён) |
| **PXE** | `67` | UDP | DHCP (`omb-dnsmasq`, Full DHCP / Proxy DHCP) | Обязательный для PXE |
| **PXE** | `4011` | UDP | Proxy DHCP (`omb-dnsmasq`) | Обязательный для Proxy DHCP |
| **PXE** | `69` | UDP | TFTP — первичные загрузчики (`undionly.kpxe`, `ipxe-shim.efi`, `ipxe.efi`, `snponly.efi`) | Обязательный для PXE |
| **PXE** | `8080` | TCP | Меню iPXE и раздача файлов WinPE / ISO по HTTP (`omb-boot`) | Обязательный для PXE |
| **PXE** | `445` | TCP | Samba-шара `[ombisos]` (раздача установочных медиа Windows и Strelec) | Обязательный для Windows/Strelec |
| **Исходящие** | `445` | TCP | Подключение внешнего SMB-хранилища (NAS) | Опциональный |
| **Исходящие** | `2049` | TCP | Подключение внешнего NFSv4.1-хранилища (NAS) | Опциональный |
| **Исходящие** | `389`, `636` | TCP | Аутентификация Active Directory / LDAP (LDAPS) | Опциональный |
| **Исходящие** | `443` | TCP | Вход через Keycloak (OIDC) | Опциональный |
| **Исходящие** | `123` | UDP | Синхронизация времени NTP | Опциональный |

Пример правил UFW для сервера с двумя интерфейсами — Management (`ens18`) и PXE (`ens19`):

```sh
# Management-интерфейс (ens18)
sudo ufw allow in on ens18 to any port 8443 proto tcp comment 'OMB Control HTTPS'
# Опционально, если включены загрузка по SFTP или Samba [iso]:
# sudo ufw allow in on ens18 to any port 2022 proto tcp comment 'OMB SFTP'
# sudo ufw allow in on ens18 to any port 445 proto tcp comment 'OMB Samba [iso]'

# PXE-интерфейс (ens19)
sudo ufw allow in on ens19 to any port 67 proto udp comment 'OMB DHCP'
sudo ufw allow in on ens19 to any port 4011 proto udp comment 'OMB Proxy DHCP'
sudo ufw allow in on ens19 to any port 69 proto udp comment 'OMB TFTP'
sudo ufw allow in on ens19 to any port 8080 proto tcp comment 'OMB iPXE HTTP'
sudo ufw allow in on ens19 to any port 445 proto tcp comment 'OMB Samba [ombisos]'
```

## Способ 1. Онлайн-установка одной командой

Рекомендуемый способ для сервера с доступом в интернет. Скрипт `scripts/install.sh` проверяет ОС и архитектуру, при интерактивном запуске предлагает выбрать канал выпусков (`stable` или `dev`, по умолчанию `stable`), скачивает `.deb`-пакет и `SHA256SUMS` из GitHub Releases, проверяет контрольную сумму, устанавливает пакет с системными зависимостями через `apt-get` и автоматически запускает интерактивный мастер `omb-setup`.

```sh
curl -fsSL https://raw.githubusercontent.com/f0nwa/oh-my-boot_/main/scripts/install.sh | sudo bash
```

Если репозиторий уже склонирован локально:

```sh
sudo bash scripts/install.sh
```

Полезные параметры `install.sh`:

- `--version <ver>` — установить конкретный релиз или тег (например `--version v0.2.0` или `--version v0.2.1-dev.1`);
- `--channel <stable|dev>` — задать канал выпусков без интерактивного запроса, если `--version` не указан (по умолчанию `stable`);
- `--deb <path>` — установить заранее скачанный локальный `.deb`-файл;
- `-- <аргументы omb-setup>` — передать дополнительные флаги напрямую в `omb-setup` (например `sudo bash scripts/install.sh -- --lang en`).

## Способ 2. Ручная онлайн-установка из `.deb`-пакета

Если вы скачали пакет `omb_<версия>_amd64.deb` вручную (например, со страницы релизов GitHub):

```sh
sudo apt update
sudo apt install ./omb_<версия>_amd64.deb
sudo omb-setup
```

Команда `apt install` подтянет системные зависимости (`postgresql`, `dnsmasq-base`, `samba`, `cifs-utils`, `nfs-common`, `wimtools` и др.), а `omb-setup` выполнит первичную инициализацию и запуск служб.

## Способ 3. Офлайн-установка (изолированный контур)

Для серверов без доступа в интернет используется подписанный локальный APT-репозиторий `apt-repo`, собранный по [руководству по офлайн-выпускам](offline-release.md).

Скопируйте на целевой сервер Ubuntu 26.04 amd64 каталог `<apt-repo>`, публичный ключ `<public-key>` и три скрипта в одну папку: `scripts/offline-deploy.sh`, `scripts/offline-install-lib.sh` и `scripts/offline-deb.py`.

**Интерактивный запуск (рекомендуется):**

```sh
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key>
```

Скрипт проверит подпись и контрольные суммы репозитория, установит системные `.deb`-пакеты и `omb` через `apt-get` внутри изолированного сетевого пространства (`unshare --net`) и передаст управление интерактивному мастеру `omb-setup --install-mode offline`.

**Позиционная форма (без интерактивного выбора сети):**

```sh
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key> <management-ip> [pxe-interface|none]
```

В этом варианте DHCP и Samba останутся выключенными до настройки PXE-сети в веб-панели. Если передать `none` или опустить четвёртый аргумент, в `/etc/omb/control.env` записывается `OMB_CONTROL_INTERFACES=omb-pxe-off` (чтобы разрешить настройку PXE через веб-панель позже, потребуется указать нужный интерфейс в `/etc/omb/control.env` и перезапустить `omb-control`). Для отложенной настройки PXE исключительно через веб-панель без ручной правки `/etc/omb/control.env` используйте интерактивный запуск `offline-deploy.sh install <apt-repo> <public-key>`: мастер автоматически заполнит `OMB_CONTROL_INTERFACES` найденными интерфейсами.

## Способ 4. Готовая ВМ для Proxmox VE / ESXi (`.qcow2.xz` и `.ova`)

В каждом релизе GitHub публикуются готовые образы ВМ на базе **Ubuntu 24.04 LTS Cloud Image** с предустановленными `omb`, PostgreSQL, Samba, `dnsmasq`, `wimtools`, `qemu-guest-agent` и Cloud-Init. Образ готов к развёртыванию и в закрытом контуре без доступа к интернету.

### Proxmox VE через скрипт (`.qcow2.xz`, рекомендуется)

Самый быстрый способ — скрипт `scripts/proxmox-create-vm.sh` на ноде Proxmox. Он сам создаёт ВМ с `VirtIO SCSI single`, `VirtIO NIC`, `qemu-guest-agent` и подключённым `CloudInit Drive`:

```sh
curl -fsSL https://raw.githubusercontent.com/f0nwa/oh-my-boot_/main/scripts/proxmox-create-vm.sh | bash -s -- \
  --vmid 200 --storage local-zfs --bridge vmbr0 --pxe-bridge vmbr1
```

Для офлайн-ноды Proxmox скачайте файл заранее и передайте его через `--image`:

```sh
bash scripts/proxmox-create-vm.sh --vmid 200 --storage local-zfs --bridge vmbr0 \
  --image omb_<версия>_ubuntu24.04_amd64.qcow2.xz
```

После создания ВМ задайте пользователя, пароль/SSH-ключ и сеть на вкладке **Cloud-Init**, запустите ВМ и выполните `sudo omb-setup`.

### Импорт `.ova` через веб-интерфейс Proxmox VE

1. **Подготовьте хранилище `local` (разово):**
   откройте **Datacenter → Storage → `local` → Edit → Content** и убедитесь, что отмечены **`Import`** и **`Disk image`**.
   - *Зачем нужен `Disk image` на `local`, если сама ВМ создаётся на `local-zfs` или `local-lvm`:* Proxmox не умеет распаковывать `.vmdk` из `.ova` напрямую в блочное хранилище (ZFS / LVM-thin). На время импорта он использует файловое хранилище (**Import Working Storage**) как временный буфер и требует на нём тип `Disk image` — без него мастер выдаст ошибку `import working storage '...' does not support 'images' content type or is not file based`. После завершения импорта временный файл удаляется автоматически, а галочку `Disk image` у `local` при желании можно снять обратно.
2. **Загрузите образ:** откройте **`local` → Import**, загрузите `omb_<версия>_ubuntu24.04_amd64.ova`, выберите файл и нажмите **Import**.
3. **Проверьте параметры в мастере импорта:**
   - вкладка **General:**
     - **Default Storage:** целевое хранилище диска ВМ (например, `local-zfs` или `local-lvm`);
     - **Import Working Storage:** оставьте **`Source Storage`** (или выберите `local`) — **не** выбирайте ZFS/LVM;
     - **Cores / Memory:** минимум `2` vCPU и `2048 MiB` (для локального хранения ISO рекомендуется `4` vCPU и `4096+ MiB`);
   - вкладка **Advanced:**
     - **Disks:** значение `Size: Unknown` у `omb-disk1.vmdk` — нормальное поведение Proxmox для `streamOptimized` VMDK, фактический размер (`20 GiB`) определится при импорте;
     - **SCSI Controller:** переключите с `Default (LSI 53C895A)` на **`VirtIO SCSI single`**;
     - **Network Interfaces (`net0`, `net1`):** смените модель с `VMware vmxnet3` на **`VirtIO (paravirtualized)`** и укажите нужные мосты (Management и PXE; если второй интерфейс не нужен, снимите галочку **Use** у `net1`).
4. **Дополнительные настройки перед первым запуском (по желанию):**
   - **Вход без Cloud-Init:** в образе уже есть консольная учётная запись **`omb`** с временным паролем **`omb`** (при первом входе система сразу попросит сменить пароль) — можно запускать ВМ сразу без добавления Cloud-Init и войти через консоль Proxmox (**noVNC**).
   - **Если нужен SSH-ключ или автонастройка IP через Cloud-Init:** добавьте **Hardware → Add → CloudInit Drive** (на `local-zfs` / `local-lvm`) и задайте **User**, **SSH public key** / **Password** и **IP Config** на вкладке **Cloud-Init**.
   - **Options → QEMU Guest Agent** — включите **Use QEMU Guest Agent** (агент уже установлен внутри образа).
   - при необходимости увеличьте диск (**Hardware → Hard Disk → Disk Action → Resize**) — корневой раздел расширится автоматически при загрузке.
5. **Запустите ВМ**, войдите в консоль под `omb` / `omb` (или по SSH, если настроили Cloud-Init) и выполните мастер настройки `sudo omb-setup`.

### Импорт `.ova` в VMware ESXi / vSphere

Импортируйте `omb_<версия>_ubuntu24.04_amd64.ova` через **Create/Register VM → Deploy a virtual machine from an OVF or OVA file**, сопоставьте логические сети `Management` и `PXE` вашим порт-группам, запустите ВМ, войдите в консоль под `omb` / `omb` (сразу смените пароль) и выполните `sudo omb-setup`.

## Шаги мастера `omb-setup`

При запуске `omb-setup` по очереди проходит шесть этапов:

1. **Шаг 0. Язык интерфейса:** выбор `Русский` или `English`. Выбранный язык используется в мастере и сразу сохраняется как язык интерфейса для учётной записи `admin`.
2. **Шаг 1. Адрес веб-панели управления (`HTTPS :8443`):** выбор IPv4-адреса из найденных сетевых интерфейсов или ввод адреса вручную.
3. **Шаг 2. Пароль первого администратора (`admin`):** ввод и подтверждение пароля (минимум 12 символов, ввод скрыт).
4. **Шаг 3. Интерфейс для сетевой загрузки (PXE):**
   - при наличии двух и более интерфейсов мастер предлагает отдельный интерфейс для PXE;
   - при одном интерфейсе поддерживается общий режим (с предупреждением и рекомендацией выбрать **Proxy DHCP**);
   - также можно выбрать «Настроить позже в веб-панели» — тогда службы поднимутся без DHCP/TFTP, а PXE вы включите позже.
5. **Шаг 4. Режим DHCP** (показывается, если выбран PXE-интерфейс):
   - **Proxy DHCP** — IP-адреса уже выдаёт внешний DHCP-сервер, OMB отвечает только на PXE-запросы;
   - **Full DHCP** — OMB сам выдаёт IP-адреса из заданного пула (начало и конец диапазона предлагаются автоматически и проверяются);
   - **Пропустить** — не запускать DHCP сейчас и включить его позже в веб-панели.
6. **Шаг 5. Сводка и подтверждение:** мастер показывает итоговую конфигурацию и после подтверждения:
   - создаёт системные учётные записи (`omb-boot`, `omb-control`, `omb-images`, `omb-iso`) и три базы PostgreSQL (`omb_boot`, `omb_images`, `omb_control`) со схемами и таблицами `schema_migrations`;
   - распаковывает закреплённые загрузочные файлы iPXE, `ipxe-shimx64.efi` и `wimboot` в `/srv/omb/tftp`;
   - генерирует `/etc/omb/secrets.key` и самоподписанный TLS-сертификат для `https://<ip>:8443`;
   - запускает службы `omb-boot`, `omb-agent`, `omb-images`, `omb-control`;
   - если на шагах 3–4 выбран PXE-режим (`proxy` или `full`), применяет конфигурацию `omb-dnsmasq` и **автоматически настраивает и запускает Samba (`smbd` и `/etc/samba/smb.conf`)** для раздачи образов Windows и Strelec.

### Неинтерактивный режим (`--non-interactive`)

Для автоматизации развёртывания `omb-setup` можно запускать без диалогов:

```sh
printf '%s\n' 'ВашНадёжныйПароль123!' | sudo omb-setup \
  --non-interactive \
  --lang ru \
  --management-ip 192.0.2.10 \
  --pxe-interface enp2s0 \
  --dhcp-mode full \
  --dhcp-start 192.0.2.100 \
  --dhcp-end 192.0.2.200 \
  --admin-password-stdin
```

Основные флаги неинтерактивного режима:

| Флаг | Назначение |
| --- | --- |
| `--non-interactive` | Запуск без интерактивных вопросов |
| `--lang <ru\|en>` | Язык вывода мастера и начальный язык `admin` (по умолчанию `ru`) |
| `--management-ip <ip>` | Обязательный IPv4-адрес веб-панели `HTTPS :8443` |
| `--pxe-interface <iface\|none>` | Сетевой интерфейс для PXE или `none` |
| `--dhcp-mode <proxy\|full\|skip>` | Режим DHCP (`proxy`, `full` или `skip`) |
| `--dhcp-start <ip>`, `--dhcp-end <ip>` | Границы пула для `--dhcp-mode full` |
| `--skip-pxe` | Пропустить настройку PXE/DHCP и настроить позже в веб-панели |
| `--admin-password-stdin` | Прочитать пароль `admin` из `stdin` |

## Первые шаги после установки

1. **Войдите в веб-панель:** откройте `https://<ip>:8443` и авторизуйтесь под пользователем `admin` с паролем, заданным в `omb-setup`.
2. **Проверьте службы и сеть:** на странице **Система → Обзор** убедитесь, что службы активны и время сервера корректно. Если настройку PXE отложили в мастере, выполните её в **Система → Сеть** — при сохранении настроек `omb-agent` автоматически применит `omb-dnsmasq` и синхронизирует конфигурацию Samba (`smbd`).
3. **Добавьте ISO-образ:** загрузите образ в **Библиотека → Образы ISO** (или подключите сетевое хранилище в **Система → Хранилища**) и выберите «Проверить ISO» в меню строки.
4. **Создайте загрузку и опубликуйте меню:** в **Библиотека → Загрузки** создайте запись на основе проверенного образа, добавьте её в черновик в разделе **Меню** и нажмите «Опубликовать меню». После этого клиенты в PXE-сети увидят загрузочный пункт.

## Консольное управление (`omb`)

После установки повседневное обслуживание сервера доступно через единую утилиту `omb`:

- `sudo omb status` — показать установленную версию, канал выпусков (`stable`/`dev`), режим установки (`online`/`offline`) и состояние всех 7 служб (`omb-control`, `omb-images`, `omb-boot`, `omb-agent`, `omb-dnsmasq`, `postgresql`, `smbd`);
- `sudo omb channel [stable|dev]` — показать текущий канал или переключить его в любой момент;
- `sudo omb update` — обновить сервер по сети из GitHub Releases с проверкой SHA-256, резервным копированием БД и применением кумулятивных SQL-миграций;
- `sudo omb reset-password [username]` — сбросить забытый пароль учётной записи `admin` (по умолчанию) или любого локального пользователя;
- `sudo omb reset-2fa [username]` — отключить 2FA и отозвать доверенные браузеры для локальной учётной записи.

## Дальнейшее чтение

- [Веб-интерфейс и администрирование (Control)](control-installation.md) — пользователи, роли, меню, журнал машин и Autoinstall.
- [Образы ISO, хранилища и профили Windows](images-installation.md) — локальные/SMB/NFS-хранилища, WinPE и драйверы.
- [Самодостаточные офлайн-выпуски, обновления и откат](offline-release.md) — сборка `apt-repo`, каналы `stable`/`dev`, `build-release.sh` и `offline-deploy.sh`.
- [Сборка офлайн-комплекта из исходников](offline-installation.md) — подготовка зависимостей и загрузочных файлов для закрытого контура.
