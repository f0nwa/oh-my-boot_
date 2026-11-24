# Самодостаточный офлайн-выпуск OMB для Ubuntu 26.04 amd64

OMB распространяется и обновляется через единый самодостаточный подписанный локальный APT-репозиторий (`apt-repo`). Каждый выпуск содержит полный пакет `omb_<версия>_amd64.deb`, все транзитивные системные `.deb`-зависимости целевой Ubuntu, индекс `Packages.gz`, манифест зависимостей `repo-manifest.json` и криптографическую подпись `repo-manifest.sig` (OpenSSL).

Промежуточные дельта-архивы и привязка к исходному коммиту (`from_commit`) не используются: обновлять можно с любой установленной версии сразу на целевую версию, включая переход через несколько промежуточных выпусков. Исторические отчёты по отработке форматов приведены в [первом прогоне](research/2026-11-21-ubuntu-offline-release-rehearsal.md) и [испытании пакетов `.deb`](research/2026-11-22-deb-package-rehearsal.md).

## Сборка и проверка `apt-repo`

Подготовьте бинарники из зафиксированного Git-коммита по [офлайн-руководству](offline-installation.md) и соберите каталог входных файлов `payload`:

- `bin/` — восемь amd64 ELF-бинарников (`omb-agent`, `omb-agentctl`, `omb-boot`, `omb-control`, `omb-controlctl`, `omb-images`, `omb-imagesctl`, `omb-winiso-prepare`);
- `deploy/systemd/` — шесть юнитов (`omb-agent.service`, `omb-boot.service`, `omb-control.service`, `omb-dnsmasq.service`, `omb-images.service`, `omb-winiso@.service`);
- `deploy/boot-assets/windows-profiles/` — профили WinPE (включая `omb-setup.cmd.in` и `winpeshl.ini`);
- `assets/` — закреплённые загрузочные файлы (`ipxeboot.tar.gz`, `ipxe-shimx64.efi`, `wimboot`);
- `scripts/prepare-ubuntu-source.sh`, `scripts/fetch-ipxe.sh`, `deploy/samba/smb.conf`;
- `deploy/sql/{boot.sql,images.sql,control.sql}` — начальные схемы трёх БД;
- `deploy/sql/migrations/{omb_boot,omb_images,omb_control}/NNNN_name.sql` — кумулятивные SQL-миграции;
- `debs/` — локальные системные `.deb`-пакеты и индекс `Packages.gz` с версиями и SHA-256.

На машине выпуска подпишите репозиторий приватным ключом OpenSSL (приватный ключ не переносится на целевые серверы, администратору отдельно передаётся публичный ключ):

```sh
python3 scripts/offline-deb.py build <payload-dir> <apt-repo> \
  --version 0.2.0 --channel stable \
  --commit e19d1f7b0a2624677593fa572654992dc30724cc \
  --license LICENSE --sign-key signing-private.pem \
  [--go-mod go.mod] [--unattend-upstream internal/unattend/UPSTREAM.md]
```

Команда `build` формирует:

- пакет `omb_<версия>_amd64.deb` (бинарники в `/usr/bin/`, юниты в `/usr/lib/systemd/system/`, помощники в `/usr/lib/omb/`, начальные схемы в `/usr/share/omb/bootstrap/`, миграции и `/usr/share/omb/migrations/manifest.json`, метаданные `/usr/share/omb/release.json` с полями `channel`, `commit`, `display_version`, `package`, `version`);
- объединённый индекс `Packages.gz` с пакетом `omb` и системными зависимостями;
- `repo-manifest.json` (формат `2`, целевая ОС `ubuntu 26.04 amd64`, метаданные пакета, контрольные суммы `Packages.gz`, системных `.deb`, загрузочных файлов, модулей из `go.mod` и закреплённого коммита `internal/unattend/UPSTREAM.md`) и подпись `repo-manifest.sig`.

Проверить подпись и целостность всех файлов репозитория без изменения системы:

```sh
python3 scripts/offline-deb.py verify-repo <apt-repo> \
  --public-key trusted-release-public.pem [--print-package]
```

С флагом `--print-package` утилита выводит строку TSV (`FILENAME`, `SHA256`, `VERSION`, `COMMIT`, `CHANNEL`), которую использует `offline-deploy.sh`.

### Сборка релиза одной командой (`scripts/build-release.sh`)

Для автоматической кросс-компиляции всех 8 бинарников, сборки `payload/`, запуска `offline-deb.py build` + `verify-repo` (и при необходимости сразу выката на стенд) используйте `scripts/build-release.sh`. Версия и канал определяются автоматически по тегу Git на `HEAD` (`vX.Y.Z` → `stable`, `vX.Y.Z-dev.N` → `dev`) или задаются через `--version` / `--channel`:

```sh
# 1. На Ubuntu-ВМ или в CI (локальный dpkg-deb)
bash scripts/build-release.sh \
  --sysdebs-dir /path/to/sysdebs \
  --assets-dir /path/to/assets \
  --sign-key /path/to/signing-private.pem \
  --out ./apt-repo

# 2. С macOS: собрать на стенде и сразу обновить его одной командой
bash scripts/build-release.sh \
  --remote fonwa@192.168.10.135 \
  --apply 192.168.10.135

# 3. С macOS: собрать на Ubuntu-хосте и забрать готовый apt-repo локально
bash scripts/build-release.sh \
  --remote fonwa@192.168.10.135 \
  --sign-key /path/to/signing-private.pem \
  --out ./dist/apt-repo
```

## Команды развёртывания, обновления и отката

Скопируйте из доверенной копии репозитория три файла в один каталог на целевой машине: `scripts/offline-deploy.sh`, `scripts/offline-install-lib.sh` и `scripts/offline-deb.py`. Передайте туда же проверенный каталог `<apt-repo>` и публичный ключ `<public-key>`.

```sh
# 1. Первичная установка на чистую Ubuntu 26.04 amd64 (запросит пароль первого admin)
sudo bash scripts/offline-deploy.sh install <apt-repo> <public-key> <management-ip> [pxe-interface|none]

# 2. Обновление (включая прыжок через несколько версий)
sudo bash scripts/offline-deploy.sh update [--allow-channel-switch] <apt-repo> <public-key> <management-ip>

# 3. Откат прерванного или последнего завершённого обновления
sudo bash scripts/offline-deploy.sh rollback <management-ip>
```

### Первичная установка (`install`)

Установка принимает только новую машину без существующих `/etc/omb`, `/srv/omb/tftp`, `/var/lib/omb-deployment` и бинарника `omb-control`. Скрипт:

1. Проверяет `apt-repo` и запускает `apt-get` в изолированном сетевом пространстве (`unshare --net`) с единственным локальным источником (если потребуется обновить уже установленный системный пакет, установка остановится).
2. Запрашивает пароль первого администратора (не менее 12 символов), создаёт системных пользователей `omb-boot`, `omb-control`, `omb-images`, `omb-iso` и три базы PostgreSQL (`omb_boot`, `omb_images`, `omb_control`).
3. Загружает начальные схемы из `/usr/share/omb/bootstrap/` и сразу регистрирует все миграции из `/usr/share/omb/migrations/manifest.json` в таблицах `schema_migrations`.
4. Распаковывает закреплённые ассеты (`ipxeboot.tar.gz`, `ipxe-shimx64.efi`, `wimboot`), создаёт `/etc/omb/secrets.key` (`0600 omb-control:omb-control`) и TLS-сертификат `/var/lib/omb-control/control.{crt,key}`.
5. Запускает `omb-boot`, `omb-agent`, `omb-images`, `omb-control` и сохраняет эталонный пакет и состояние в `/var/lib/omb-deployment/` (`current.deb`, `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `COMMIT`).

DHCP (`omb-dnsmasq`) и Samba после первичной установки выключены; настройку статического PXE-адреса, DHCP и шары выполняйте по [руководству сервера](installation.md). Если передать `none` (или опустить четвёртый аргумент), Control стартует с зарезервированным именем `omb-pxe-off`, которое можно позже заменить на физический интерфейс через `OMB_CONTROL_INTERFACES` и перезапуск `omb-control`.

### Локальный кеш `current.deb` и вычисление области изменений

При установке и каждом успешном обновлении копия текущего пакета сохраняется в `/var/lib/omb-deployment/current.deb` (рядом с `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `COMMIT` и `LAST`).

Во время `offline-deploy.sh update`:

1. Проверяются подпись `apt-repo`, отсутствие маркера `PENDING`, версия (`dpkg --compare-versions new gt old`), совпадение канала и соответствие установленного в системе пакета файлу `current.deb` (`dpkg --verify omb` и `/var/lib/dpkg/info/omb.md5sums`).
2. Содержимое `current.deb` сравнивается с новым `.deb`, а строки `schema_migrations` всех трёх БД сравниваются с `/usr/share/omb/migrations/manifest.json`.
3. Автоматически вычисляется область изменений (`scope`): какие службы (`control` всегда включён; `images`, `boot`, `agent`, `dnsmasq` — при изменении их файлов или БД) и какие БД (`omb_boot`, `omb_images`, `omb_control` или `none`) затронуты. Незатронутые службы продолжают работать без перезапуска.
4. Обновления не перезаписывают `/etc/omb/`, `/srv/omb/tftp/` и входные ресурсы развёртывания (`/usr/share/omb/{assets,samba,windows-profiles}/` — попытка изменить их через обновление отклоняется).

### Кумулятивные миграции и `schema_migrations`

Миграции хранятся в репозитории по пути `deploy/sql/migrations/<db>/NNNN_<name>.sql` для баз `omb_boot`, `omb_images`, `omb_control` и подчиняются правилам append-only:

- имя файла должно соответствовать шаблону `NNNN_name.sql`, префиксы `NNNN` внутри одной БД уникальны;
- внутри SQL-файла запрещены `BEGIN`, `COMMIT`, `ROLLBACK` и `VACUUM` — установщик оборачивает каждую миграцию в транзакцию и добавляет запись в `schema_migrations (id, sha256)`;
- уже применённые на сервере миграции из `schema_migrations` обязаны присутствовать в новом пакете с тем же SHA-256, а неприменённые миграции не могут иметь `id` меньше уже применённой.

Благодаря этому обновление работает кумулятивно даже при прыжке через несколько релизов подряд: применяются ровно те миграции (в порядке `omb_boot` → `omb_images` → `omb_control`), которых ещё нет в `schema_migrations`.

### Безопасность обновления и откат (`rollback`)

Перед изменением пакета и БД `offline-deploy.sh update`:

1. Проверяет запас свободного места для дампов и тестовых копий БД.
2. Снимает текущий опубликованный `menu.ipxe` и выставляет маркер `/var/lib/omb-deployment/PENDING`.
3. Останавливает только затронутые службы, сохраняет предыдущий `omb.deb`, конфиги из `/etc/omb/`, контрольные слепки пользователей/каталога/журнала и выполняет `pg_dump -Fc` с обязательным тестовым восстановлением в отдельные базы `omb_restore_*` в каталоге `/var/backups/omb/offline.<id>`.
4. Ставит новый `.deb`, применяет недостающие миграции, запускает службы и проверяет их состояние, сохранность пользователей и неизменность опубликованного PXE-меню.
5. Обновляет `current.deb`, `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL`, `COMMIT`, `LAST` и удаляет `PENDING`.

При любой ошибке (или при вызове `sudo bash scripts/offline-deploy.sh rollback <management-ip>` после прерывания или для отмены `LAST`-обновления) установщик:

- возвращает сохранённый в резервной копии предыдущий `.deb` (`apt-get --allow-downgrades --reinstall`);
- в одной транзакции PostgreSQL переключает затронутые базы обратно на проверенные `omb_restore_*`, сохраняя мигрированные базы под именами `omb_retained_*` с `ALLOW_CONNECTIONS false`;
- проверяет службы, меню `omb-boot` и восстанавливает `current.deb` и маркеры состояния.

Если после снимка изменились `/etc/omb/secrets.key` или опубликованное меню `menu.ipxe`, откат остановится и потребует согласованного восстановления ключа, меню и БД.

## Каналы обновлений (`stable` и `dev`)

OMB поддерживает два параллельных канала выпусков:

- **`stable` (ветка `main`):** теги `vX.Y.Z`, пакеты `.deb` с версиями `X.Y.Z` (например `0.2.0`).
- **`dev` (ветка `dev`):** теги `vX.Y.Z-dev.N`, пакеты `.deb` с версиями `X.Y.Z~dev.N` (тильда `~` гарантирует `0.2.0~dev.N < 0.2.0` в `dpkg`).

Текущий канал хранится в `/var/lib/omb-deployment/CHANNEL` и отражается в `/usr/share/omb/release.json` и в веб-интерфейсе. По умолчанию `offline-deploy.sh update` разрешает обновление только внутри текущего канала; для явного перехода (`stable` ↔ `dev`) передайте `--allow-channel-switch`:

```sh
sudo bash scripts/offline-deploy.sh update --allow-channel-switch \
  <apt-repo> <public-key> <management-ip>
```
