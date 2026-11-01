# Images: каталог ISO и хранилища OMB

[English version](en/images-installation.md). Порядок работы с установками и меню — в [руководстве Control](control-installation.md#установки-и-меню).

Images хранит каталог ISO в базе `omb_images` и обслуживает локальный API `/run/omb-images/images.sock`. Найденный ISO ещё не является загрузочным пунктом: сначала нужен подготовленный профиль, затем установка и публикация меню.

## Первая установка

Сначала подготовьте Boot, Agent и PostgreSQL по [руководству сервера](installation.md). Следующие команды предназначены для нового сервера. На существующем стенде сохраняйте базу, настройки и зарегистрированные хранилища.

Сборка в корне репозитория:

~~~sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-images ./cmd/omb-images
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o dist/omb-imagesctl ./cmd/omb-imagesctl
~~~

Перенесите `dist` и `deploy` на сервер. Серверные команды выполняются из каталога с этими файлами в Bash под root:

~~~sh
install -m 0755 dist/omb-images dist/omb-imagesctl /usr/local/bin/
useradd --system --user-group --home-dir /var/lib/omb-images --shell /usr/sbin/nologin omb-images
install -d -m 0750 -o root -g root /etc/omb
install -d -m 0750 -o omb-images -g omb-images /var/lib/omb-images
install -d -m 2770 -o root -g omb-images /var/lib/omb-images/iso
install -d -m 0755 -o root -g root /mnt/omb-smb-upload
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c 'CREATE ROLE "omb-images" LOGIN'
runuser -u postgres -- createdb --owner=omb-images omb_images
runuser -u omb-images -- psql -v ON_ERROR_STOP=1 --dbname=omb_images < deploy/sql/images.sql
~~~

Создание настроек защищено от перезаписи:

~~~sh
(
  set -eu
  test ! -e /etc/omb/images.env
  cat > /etc/omb/images.env <<'ENV'
OMB_IMAGES_DATABASE_URL="host=/var/run/postgresql dbname=omb_images user=omb-images"
ENV
  chmod 0640 /etc/omb/images.env
)
install -m 0644 deploy/systemd/omb-images.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now omb-images.service
~~~

Установите Control по [его руководству](control-installation.md). Его юнит содержит `SupplementaryGroups=omb-images`, чтобы обращаться к сокету Images. При изменении групп юнита уже работающую службу нужно перезапустить.

## Регистрация и сканирование

Созданный локальный каталог регистрируется один раз:

~~~sh
runuser -u omb-images -- env OMB_IMAGES_DATABASE_URL='host=/var/run/postgresql dbname=omb_images user=omb-images' \
  /usr/local/bin/omb-imagesctl add-path -name Local -kind local -root /var/lib/omb-images/iso
~~~

`-kind mounted` предназначен для уже смонтированной файловой системы: нужен абсолютный путь к настоящей точке монтирования без символьных ссылок. Управляемое SMB-подключение добавляйте через «Система → Хранилища», указывая прямой путь вроде `//nas/share/iso` и учётные данные. Для SMB нужен `cifs-utils`; Agent хранит отдельные файлы учётных данных в `/etc/omb/smb` с правами `0600`.

Оператор и администратор могут запускать сканирование, наблюдатель видит каталог. Проверка доступности работает в фоне; при ошибке прежние записи сохраняются. Прерванное перезапуском сканирование отмечается ошибкой, после чего его можно повторить. Восстановление управляемых монтирований описано в [руководстве Control](control-installation.md#доступность-хранилищ-и-восстановление).

## Загрузка ISO

В библиотеке выберите доступное для записи хранилище, существующую папку и ISO до 32 ГиБ. Полный ISO передаётся на выбранное хранилище; после передачи проверяются размер и SHA-256. Существующий файл не перезаписывается, отмена удаляет временный файл. Публикация меню выполняется отдельно.

Для SMB используются два подключения: `/mnt/omb-smb/<ключ>` для чтения и `/mnt/omb-smb-upload/<ключ>` для записи. Учётная запись NAS должна разрешать запись. Локальная загрузка использует `/var/lib/omb-images/iso`; группа `omb-images` должна иметь право записи, а юнит — разрешать этот путь. Свободное место показывается для файловой системы выбранного хранилища.

## Профили Windows, XML и драйверы

В библиотеке создайте профиль из Windows x64 ISO и нажмите «Подготовить». Agent проверяет источник, монтирует ISO только для чтения и готовит загрузочные файлы в `/srv/omb/tftp/windows-profiles/<ID>/`. Полный ISO остаётся на хранилище; клиент получает установочные файлы через отдельную шару `ombisos` и учётную запись `omb-iso`. Учётные данные внешнего NAS в WinPE не передаются.

Для подготовки нужны установленный `omb-winiso-prepare`, [юнит профиля](../deploy/systemd/omb-winiso@.service), [шаблоны](../deploy/boot-assets/windows-profiles/) в `/usr/share/omb/windows-profiles`, конфигурация [Samba](../deploy/samba/smb.conf) и пароль `/etc/omb/omb-iso-password`. Каталоги монтирования и конфигурации: `/mnt/omb-iso/profiles`, `/etc/omb/winiso`. Эти компоненты устанавливаются в составе [серверного комплекта](offline-installation.md); одна установка Images их не заменяет. Локальные исходные ISO для Windows должны находиться внутри `/var/lib/omb-images` либо на управляемом SMB-хранилище.

Загрузите проверенный Windows XML в «Библиотека → Файлы ответов», а INF с сопутствующими файлами — в «Библиотека → Драйверы». Создайте установку с нужным профилем, XML и INF, дождитесь подготовки и опубликуйте её в меню. Исходный ISO остаётся прежним. Подробности — в [руководстве Control](control-installation.md#каталог-драйверов-winpe).

XML может содержать пароли и ключи. Загрузочные файлы предназначены для доверенной PXE-подсети. Известное ограничение доступа к сценариям конфигураций драйверов ещё требует исправления: см. [отчёт стенда](research/2026-10-31-omb-technical-migration.md#существующая-проблема-доступа).

## Диагностика

Выполняется под root; явная группа нужна для проверки сокета от имени Control вне systemd:

~~~sh
systemctl is-active omb-images omb-control omb-boot omb-agent omb-dnsmasq
runuser -u omb-images -- psql -XAt --dbname=omb_images -c 'SELECT 1'
runuser -u omb-control -g omb-images -- curl --fail --unix-socket /run/omb-images/images.sock http://unix/catalog
journalctl -u omb-images -u omb-agent -n 100 --no-pager
~~~

Перед обновлением сохраните согласованную копию баз, конфигурации и файлов. [Восстановление](recovery.md), [состояние стенда и оставшиеся проверки](research/2026-10-31-omb-technical-migration.md), [доступ к прежним отчётам](project-history.md).
