# Установка без доступа в интернет

[English version](en/offline-installation.md)

Обычные руководства рассчитаны на сервер с выходом в интернет: пакеты ставятся через `apt`, Go-модули скачиваются при сборке, iPXE и wimboot загружаются с GitHub. Этот документ описывает, как на машине с интернетом собрать комплект со всем необходимым, перенести его на изолированный сервер и установить там без обращений наружу.

Здесь описаны только замены шагов, которые что-то скачивают. Настройка служб (`boot.env`, `control.env`, DHCP, хранилища, Samba) остаётся такой же, как в [установке Boot](installation.md), [руководстве Control](control-installation.md) и [руководстве Images](images-installation.md).

> Порядок пока не проверен на сервере, полностью отрезанном от интернета. После первой такой установки допишите сюда результат.

## Что скачивается при обычной установке

| Что | Откуда при обычной установке | Чем заменяется |
| --- | --- | --- |
| Пакеты Ubuntu | `apt` из репозиториев Ubuntu | `.deb` из комплекта, локальный репозиторий |
| Go 1.26 и Go-модули (`pgx`, `golang.org/x/...`) | go.dev и proxy.golang.org при `go build` | Готовые статические бинарные файлы (`CGO_ENABLED=0`), Go на сервере не нужен |
| iPXE v2.0.0 `ipxeboot.tar.gz` | GitHub через `scripts/fetch-ipxe.sh` | Архив из комплекта, тот же скрипт с адресом `file://` |
| wimboot 2.9.0 | GitHub | Файл из комплекта |
| Драйверы virtio-win (только для ВМ) | Зеркало Fedora | ZIP из комплекта, импорт через страницу «Драйверы» |

Админка ничего не загружает извне: стили, скрипты и шрифты встроены в бинарный файл Control. ISO операционных систем берутся с подключённых хранилищ (SMB или местный каталог), а не из интернета. Их нужно заранее положить на NAS или в местное хранилище.

## Что нужно

- **Сервер:** Ubuntu Server 26.04.1 LTS x86_64, на ней проверены остальные руководства. Пакеты в комплекте подходят только для того же выпуска Ubuntu и той же архитектуры. Для другого выпуска соберите комплект заново.
- **Машина для сборки:** доступ в интернет, Go 1.26 или новее, Docker (на Apple Silicon контейнер запускается с `--platform linux/amd64`), `git`, `curl`. Подойдёт и macOS.
- **Носитель или канал** для переноса комплекта; его размер — порядка сотен мегабайт без драйверов.

## 1. Собрать комплект

Готовый комплект выглядит так:

```text
pxe-offline/
  COMMIT                    коммит, из которого собраны файлы
  bin/                      восемь бинарных файлов Linux x86_64
  debs/                     .deb со всеми зависимостями и индекс Packages.gz
  assets/                   ipxeboot.tar.gz, wimboot, expected.sha256
  pxe-platform-src.tar.gz   deploy/, scripts/ и docs/ того же коммита
  SHA256SUMS
```

Все команды этого раздела выполняются из корня репозитория.

### 1.1 Бинарные файлы

Собирайте из чистого рабочего дерева, чтобы бинарные файлы и `deploy/` относились к одному коммиту:

```sh
test -z "$(git status --porcelain)" || { echo 'есть незакоммиченные изменения'; exit 1; }
go test ./... && go vet ./...

KIT=$HOME/pxe-offline
mkdir -p "$KIT/bin"
git rev-parse HEAD > "$KIT/COMMIT"
for c in pxe-boot pxe-agent pxe-agentctl pxe-control pxe-controlctl \
         pxe-images pxe-imagesctl pxe-winiso-prepare; do
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o "$KIT/bin/$c" ./cmd/$c
done
```

С `CGO_ENABLED=0` файлы статические и не зависят от библиотек сервера.

Если правила требуют собирать на изолированной стороне, положите в комплект Go `go1.26.x.linux-amd64.tar.gz` с [go.dev/dl](https://go.dev/dl/) и копию исходников с модулями. Модули добавляются командой `go mod vendor` в отдельной копии репозитория; каталог `vendor/` в репозиторий не коммитится. На сервере собирайте с `GOFLAGS=-mod=vendor GOTOOLCHAIN=local`, иначе Go попытается скачать модули или другую версию компилятора.

### 1.2 Пакеты Ubuntu

Каким службам нужны пакеты:

| Пакет | Зачем |
| --- | --- |
| `dnsmasq-base` | DHCP, proxy-DHCP и TFTP (`pxe-dnsmasq.service`) |
| `postgresql` | Базы `pxe_boot`, `pxe_control`, `pxe_images` |
| `ca-certificates`, `curl` | `fetch-ipxe.sh` и проверки HTTP из руководств |
| `openssl` | Самоподписанный сертификат админки |
| `cifs-utils` | Монтирование SMB-хранилищ с ISO |
| `samba` | Папка `pxeisos`, с которой WinPE запускает Setup; `smbstatus` для журнала Agent |
| `wimtools` | `wimlib-imagex` для WIM с драйверами |

Пакеты скачиваются в чистом контейнере того же выпуска Ubuntu. В контейнере установлено меньше, чем на Ubuntu Server, поэтому в комплект попадут все зависимости, даже те, что на сервере уже есть. Для них же строится индекс локального репозитория:

```sh
mkdir -p "$KIT/debs"
docker run --rm --platform linux/amd64 -v "$KIT/debs:/out" ubuntu:26.04 sh -ec '
  apt-get update
  apt-get install -y --download-only --no-install-recommends \
    dnsmasq-base postgresql ca-certificates curl openssl cifs-utils samba wimtools
  cp /var/cache/apt/archives/*.deb /out/
  apt-get install -y --no-install-recommends dpkg-dev
  cd /out && dpkg-scanpackages --multiversion . /dev/null | gzip -9c > Packages.gz'
```

Собирайте комплект незадолго до установки. Если сервер обновлялся позже сборки комплекта, `apt` может не найти подходящих версий зависимостей; тогда соберите пакеты заново.

### 1.3 Загрузочные файлы

```sh
mkdir -p "$KIT/assets" && cd "$KIT/assets"
curl -fL -o ipxeboot.tar.gz https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz
curl -fL -o wimboot https://github.com/ipxe/wimboot/releases/download/v2.9.0/wimboot
cat > expected.sha256 <<'SUMS'
01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1  ipxeboot.tar.gz
5f067ccdc4d084d5bf77b6c853bd0f8402dfc2b4cd1b103d358993ae97fae8e3  wimboot
SUMS
sha256sum -c expected.sha256      # на macOS: shasum -a 256 -c expected.sha256
cd -
```

Хеши взяты из [описания загрузочных файлов](../deploy/boot-assets/README.md) и [руководства Windows 10](windows-winpe-pxe.md). Если проверка не прошла, не используйте файлы.

**Драйверы для ВМ (необязательно).** Для Windows на Proxmox/KVM нужны драйверы virtio-win. Скачайте ISO выпуска 0.1.271 по ссылке и с хешем из [руководства Windows 10](windows-winpe-pxe.md), извлеките нужные папки (например, `vioscsi/w11/amd64` и `NetKVM/w11/amd64`) и упакуйте их в ZIP в `$KIT/assets/drivers/`. На сервере этот ZIP импортируется на странице «Драйверы».

### 1.4 Исходники и контрольные суммы

```sh
git archive --format=tar.gz -o "$KIT/pxe-platform-src.tar.gz" HEAD
cd "$KIT"
find . -type f ! -name SHA256SUMS | sort | xargs sha256sum > SHA256SUMS   # macOS: xargs shasum -a 256
cd ..
tar -czf "pxe-offline-$(cat pxe-offline/COMMIT | cut -c1-8).tar.gz" pxe-offline
```

Хеш итогового архива запишите отдельно от носителя, например в заявку на перенос.

## 2. Перенести на сервер

Скопируйте архив на сервер любым разрешённым способом и распакуйте в `/opt`:

```sh
sudo tar -xzf pxe-offline-*.tar.gz -C /opt
sudo chown -R root:root /opt/pxe-offline
sudo chmod -R u=rwX,go=rX /opt/pxe-offline
cd /opt/pxe-offline && sha256sum -c --quiet SHA256SUMS && echo 'комплект цел'
```

Права `go=rX` нужны и для `apt`: он читает локальный репозиторий от служебного пользователя `_apt`.

## 3. Установить

### 3.1 Пакеты

Подключите каталог `debs` как локальный репозиторий и обновите индекс только по нему. Остальные источники при этом не опрашиваются:

```sh
echo 'deb [trusted=yes] file:/opt/pxe-offline/debs ./' | sudo tee /etc/apt/sources.list.d/pxe-offline.list
sudo apt-get update \
  -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/pxe-offline.list \
  -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0
sudo apt-get install -y --no-install-recommends \
  dnsmasq-base postgresql ca-certificates curl openssl cifs-utils samba wimtools
```

`trusted=yes` отключает проверку подписи репозитория. Это допустимо, потому что целостность комплекта уже проверена по `SHA256SUMS`. После установки файл `/etc/apt/sources.list.d/pxe-offline.list` можно удалить.

После установки пакета `samba` служба `smbd` сразу запускается с конфигурацией Ubuntu по умолчанию. Замените `/etc/samba/smb.conf` по [образцу](../deploy/samba/smb.conf), исправив `interfaces` и `hosts allow` под свою сеть, до того как подключать сервер к сети клиентов.

### 3.2 Бинарные файлы и исходники

```sh
sudo install -m 0755 /opt/pxe-offline/bin/* /usr/local/bin/
sudo install -d -m 0755 /opt/pxe-platform-src
sudo tar -xzf /opt/pxe-offline/pxe-platform-src.tar.gz -C /opt/pxe-platform-src
cd /opt/pxe-platform-src
```

Дальше все команды с путями `deploy/...` и `scripts/...` выполняются из `/opt/pxe-platform-src`.

### 3.3 iPXE и wimboot

`fetch-ipxe.sh` скачивает архив через `curl`, а `curl` понимает адреса `file://`. Скрипт так же проверяет SHA-256 и не перезаписывает существующий каталог:

```sh
sudo ./scripts/fetch-ipxe.sh \
  file:///opt/pxe-offline/assets/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  /srv/pxe/tftp
```

Меню Windows загружает `wimboot` по адресу `/assets/windows/wimboot`, то есть из `/srv/pxe/tftp/windows/wimboot`:

```sh
sudo install -d -m 0755 /srv/pxe/tftp/windows
sudo install -m 0644 /opt/pxe-offline/assets/wimboot /srv/pxe/tftp/windows/wimboot
echo '5f067ccdc4d084d5bf77b6c853bd0f8402dfc2b4cd1b103d358993ae97fae8e3  /srv/pxe/tftp/windows/wimboot' | sha256sum -c
```

### 3.4 Пользователи и базы данных

Системные пользователи создаются так же, как в руководствах; группа `pxe-control` нужна до запуска Boot и Agent, потому что их юниты используют её как `Group=`:

```sh
sudo useradd --system --user-group --home /var/lib/pxe-platform-control --shell /usr/sbin/nologin pxe-control
sudo useradd --system --user-group --home /var/lib/pxe-platform-images --shell /usr/sbin/nologin pxe-images
sudo useradd --system --home /var/lib/pxe-platform-boot --shell /usr/sbin/nologin pxe-boot
for svc in boot control images; do
  sudo runuser -u postgres -- psql -c "CREATE ROLE \"pxe-$svc\" LOGIN"
  sudo runuser -u postgres -- createdb -O "pxe-$svc" "pxe_$svc"
done
```

В руководствах миграции описаны как обновления уже работающего сервера. Для пустых баз большая часть миграций уже входит в основные схемы, поэтому порядок другой:

```sh
sudo runuser -u pxe-boot    -- psql -v ON_ERROR_STOP=1 -d pxe_boot    < deploy/sql/boot.sql
sudo runuser -u pxe-boot    -- psql -v ON_ERROR_STOP=1 -d pxe_boot    < deploy/sql/2026-10-23-boot-transfer.sql
sudo runuser -u pxe-images  -- psql -v ON_ERROR_STOP=1 -d pxe_images  < deploy/sql/images.sql
sudo runuser -u pxe-control -- psql -v ON_ERROR_STOP=1 -d pxe_control < deploy/sql/control.sql
sudo runuser -u pxe-control -- psql -v ON_ERROR_STOP=1 -d pxe_control < deploy/sql/2026-10-19-winpe-drivers.sql
```

- `boot.sql` не содержит колонок `status`, `bytes` и `size`, поэтому миграция `2026-10-23-boot-transfer.sql` нужна.
- `control.sql` не содержит таблиц каталога драйверов, поэтому нужна `2026-10-19-winpe-drivers.sql`. Остальные миграции Control уже входят в `control.sql`; их повторный запуск ничего не меняет.
- **Не применяйте** `2026-10-15-managed-smb.sql` к новой базе Images: её колонки уже есть в `images.sql`, и миграция завершится ошибкой.

Новые миграции, добавленные после этого документа, проверяйте так же: если основная схема их не содержит, примените их после схемы.

### 3.5 Остальная настройка

Дальше настройка не отличается от обычной:

- [Установка Boot](installation.md): юниты, `boot.env`, проверка меню, применение DHCP через `pxe-agentctl`. Шаги `apt` и `fetch-ipxe.sh` уже выполнены выше.
- [Руководство Control](control-installation.md): `control.env`, сертификат, первый администратор. Сертификат создаётся локально, без обращений в сеть, например:
  ```sh
  sudo install -d -m 0700 -o pxe-control -g pxe-control /var/lib/pxe-platform-control
  sudo openssl req -x509 -newkey rsa:3072 -nodes -days 90 -subj '/CN=pxe-control' \
    -addext 'subjectAltName=IP:<адрес админки>' \
    -keyout /var/lib/pxe-platform-control/control.key -out /var/lib/pxe-platform-control/control.crt
  sudo chown pxe-control:pxe-control /var/lib/pxe-platform-control/control.*
  sudo chmod 0600 /var/lib/pxe-platform-control/control.key
  ```
- [Руководство Images](images-installation.md): `images.env`, хранилища, профили Windows, шаблоны `deploy/boot-assets/windows-profiles/` в `/usr/share/pxe-platform/windows-profiles`, пользователь Samba `pxe-iso`.

`pxe-agent.service` работает с `ProtectSystem=strict` и не запустится, если отсутствует хоть один путь из его `ReadWritePaths`. До первого запуска должны существовать `/etc/pxe-platform`, `/etc/pxe-platform/winiso`, `/mnt/pxe-platform-smb`, `/mnt/pxe-platform-smb-upload`, `/mnt/pxe-platform-iso/profiles`, `/srv/pxe/tftp/windows-profiles`, `/srv/pxe/tftp/windows-answers`, `/srv/pxe/tftp/windows-driver-wims` и `/srv/pxe/tftp/windows-driver-configs`. Владельцы и права указаны в руководстве Images и в разделе о драйверах руководства Control.

## 4. Проверить

Сначала убедитесь, что сервер действительно изолирован, иначе проверка ничего не докажет:

```sh
curl -sS -m 5 -o /dev/null https://proxy.golang.org && echo 'ВНИМАНИЕ: интернет доступен'
```

Затем проверьте службы и меню так же, как в руководствах:

```sh
systemctl is-active postgresql pxe-boot pxe-agent pxe-control pxe-images smbd
curl -fsS http://<адрес Boot>:8080/boot/menu.ipxe | head -1        # ожидается #!ipxe
curl -fsSI http://<адрес Boot>:8080/assets/windows/wimboot | head -1
```

## 5. Эксплуатация без интернета

- **Время.** Без интернета сервер не синхронизирует часы с NTP Ubuntu. Сбитые часы ломают срок действия сертификата и порядок событий в журналах. Укажите NTP-сервер в локальной сети в `/etc/systemd/timesyncd.conf` (`NTP=<адрес>`), например роутер, и перезапустите `systemd-timesyncd`.
- **Обновления безопасности.** `apt` и `unattended-upgrades` на изолированном сервере ничего не получат. Обновления Ubuntu приходят только с новым комплектом.
- **Обновление PXE Platform.** Соберите новый комплект из нового коммита. Новые миграции — это файлы, добавленные в `deploy/sql` между коммитами: `git diff --name-only --diff-filter=A <старый>..<новый> -- deploy/sql`. Перед заменой сохраните прежние бинарные файлы и дамп баз, как описано в руководствах, затем примените миграции и замените бинарные файлы. Пакеты из комплекта ставятся только если их список изменился или нужны обновления.
