---
slug: windows-winpe
section: os-scenarios
order: 10
title: Установка Windows 10/11 через WinPE
summary: Как OMB готовит и загружает образы Windows 10 и 11 по сети через wimboot, WinPE и изолированную шару Samba.
keywords: [windows, winpe, wimboot, omb-setup.cmd, ombisos, setup.exe, iso, media not ready, retrying, wpeinit]
---

OMB разворачивает Windows 10 и Windows 11 (проверено в том числе на Windows 11 26H2) в Legacy BIOS и UEFI (с Secure Boot и без) без использования WDS и без модификации исходных ISO-образов.

## Как работает подготовка профиля

1. Вы загружаете или подключаете ISO-образ Windows на локальном, SMB- или NFS-хранилище и запускаете «Проверить ISO» на странице [Библиотека → Образы ISO](/images).
2. При создании загрузки в [Библиотека → Загрузки](/installations) `omb-agent` подготавливает профиль Windows (`omb-winiso@.service`):
   - монтирует ISO только для чтения (`mount -o loop,ro`) в `/mnt/omb-iso/profiles/<id>`;
   - извлекает минимальный комплект для старта WinPE (`boot.wim`, `BCD`, `boot.sdi`) в `/srv/omb/tftp/windows-profiles/<id>/`;
   - публикует смонтированное содержимое ISO под случайным токеном внутри read-only шары Samba `[ombisos]`.
3. `omb-setup.cmd` генерируется из шаблона `/etc/omb/windows-profiles/omb-setup.cmd.in` с актуальными параметрами ожидания сети и действием после завершения Windows Setup.

> [!IMPORTANT]
> Доступ к `omb-setup.cmd` и файлам ответов XML по HTTP ограничен загрузочной подсетью (`Cache-Control: no-store`). Из сети управления эти URL возвращают `404`.

## Что происходит на клиенте в WinPE

После выбора пункта Windows в меню iPXE клиент:

1. Загружает `/assets/windows/wimboot` (версия `v2.9.0`), `boot.wim`, `BCD`, `boot.sdi` и `omb-setup.cmd` по HTTP в оперативную память.
2. Запускает WinPE и выполняет `omb-setup.cmd`.
3. Инициализирует сеть (`wpeinit`) и циклически пытается подключить шару `\\<IP-OMB>\ombisos\<токен>` на диск `I:` с учётной записью `omb-iso` (только чтение).
4. Как только сеть поднялась и шара подключена, цикл завершается сразу, не дожидаясь окончания лимита.
5. Если к загрузке привязан файл ответов, он копируется в `X:\autounattend.xml` (а при режиме авторазметки запускается `pe.cmd`), после чего стартует `I:\setup.exe`.

## Настройки ожидания сети и выхода из Setup

В [Система → Сеть](/network) в блоке **WinPE (Windows Setup)** настраиваются три параметра:

- **Лимит ожидания сети и шары (`winpe_timeout_seconds`):** от 10 до 600 с, по умолчанию 90 с.
- **Пауза между попытками (`winpe_retry_seconds`):** от 1 до 30 с, по умолчанию 2 с.
- **Действие после закрытия Windows Setup:** перезагрузка, выключение или командная строка WinPE (удобно при отладке).

При сохранении новых значений `omb-agent` атомарно обновляет шаблон, перегенерирует все готовые `omb-setup.cmd` (включая сервисные образы Strelec), пересчитывает их `script_sha256` и обновляет активное меню без перезапуска `dnsmasq`.

## Что пишет консоль WinPE (`omb-setup.cmd`)

Во время старта WinPE на экране отображается ход сценария `omb-setup.cmd`:

1. `OMB: initializing network...` — запуск `wpeinit` и `wpeutil WaitForNetwork` (лог пишется в `%SystemRoot%\System32\omb-network-wait.log`).
2. `OMB: network is not ready yet; waiting for installation media...` — `WaitForNetwork` завершился до получения IP или готовности порта, скрипт переходит к циклу ожидания.
3. `OMB: connecting installation media...` и строки вида:
   `OMB: media not ready (error <код>); retrying 10/45...`
4. `OMB: starting Windows Setup...` — шара подключена, файл `install.wim` / `install.esd` найден, запущен `setup.exe`.

> [!NOTE]
> Повторы `retrying 1/45…10/45` с последующим запуском установки — **штатная работа**, а не ошибка. По умолчанию лимит ожидания 90 с при паузе 2 с (`45` попыток). Пока поднимается сетевая карта, отрабатывает порт коммутатора (STP / PortFast) или выдаётся адрес по DHCP, скрипт повторяет `net use`, и как только сеть готова — сразу запускает установку.

Если счётчик дошёл до последней попытки (`45/45`) и появилось `OMB: installation media is unavailable or Setup failed.`, скрипт оставляет открытой консоль `cmd`. На что смотреть:

- **Код в скобках `error <N>`** (код возврата `net use` / системной ошибки Windows):
  - `53` или `1231` — нет сети, клиент не получил IP, в `boot.wim` нет драйвера сетевой карты или на сервере закрыт TCP 445 / политика доступа PXE-клиентов;
  - `5`, `86` или `1326` — ошибка авторизации учётной записи `omb-iso` в Samba;
  - ошибок `net use` не было, но сразу переход в `failed` — на диске не найден `sources\install.wim` / `install.esd` (проверьте монтирование ISO на сервере).
- **Быстрая диагностика в той же консоли WinPE:**
  - `ipconfig /all` — получил ли адаптер IP-адрес и определилась ли сетевая карта;
  - `ping <IP-OMB>` — есть ли связность с PXE-сервером;
  - `type %SystemRoot%\System32\omb-network-wait.log` — вывод `wpeutil WaitForNetwork`.

## Частые причины сбоев в WinPE

- **WinPE не получает IP или не видит шару:** в `boot.wim` нет драйвера сетевой карты этой модели. Загрузите INF-драйвер в [Библиотека → Драйверы](/drivers) и подключите его к загрузке (см. [Драйверы WinPE и сервисный образ Strelec](/wiki/drivers-and-strelec)).
- **Установщик Windows не видит NVMe/RAID-диск:** добавьте драйвер контроллера хранения (например, Intel VMD / RST или VirtIO SCSI) в ту же конфигурацию драйверов WinPE.
- **Шара `ombisos` недоступна после сбоя NAS:** проверьте статус исходного хранилища в [Система → Хранилища](/storages) — если ISO лежит на внешнем SMB/NFS и связь обрывалась, выполните переподключение хранилища.
