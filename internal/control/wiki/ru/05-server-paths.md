---
slug: server-paths
section: architecture
order: 30
title: Карта каталогов и портов сервера
summary: Где на сервере хранятся конфигурации, ключи, образы ISO, загрузочные файлы TFTP, журналы и какие сетевые порты использует OMB.
keywords: [пути, каталоги, порты, /etc/omb, /srv/omb, /var/lib, /mnt, файрвол]
---

Справочник файловой структуры и сетевых портов сервера OMB для администрирования, резервного копирования и настройки межсетевого экрана.

## Основные каталоги сервера

| Путь | Назначение | Кто пишет |
| --- | --- | --- |
| `/usr/local/bin/omb-*` | Исполняемые файлы служб и утилит OMB | Пакет `omb` (`.deb`) |
| `/usr/lib/systemd/system/omb-*.service` | Юниты systemd служб OMB | Пакет `omb` (`.deb`) |
| `/usr/share/omb/` | Шаблоны `windows-profiles/` и метаданные выпуска `release.json` | Пакет `omb` (`.deb`) |
| `/etc/omb/` | Конфигурации служб (`boot.env`, `control.env`, `images.env`, `dnsmasq.conf`), ключ шифрования `secrets.key` (`0600`), `omb-iso-password`, `samba-usermap.conf`, учётные данные `smb/` и `nfs/`, шаблон `windows-profiles/omb-setup.cmd.in` | Администратор / `omb-agent` |
| `/var/lib/omb-control/` | TLS-сертификат и приватный ключ веб-панели (`control.crt`, `control.key` `0600`) | Администратор |
| `/var/lib/omb-boot/` | Снимок опубликованного меню `menu.ipxe` | `omb-boot` |
| `/var/lib/omb-dhcp/hosts` | Индивидуальные правила драйверов UEFI по MAC/OUI и `<mac>,ignore` | `omb-agent` |
| `/var/lib/omb-images/iso/` | Локальное хранилище образов ISO (также корень шары SMB `[iso]` и SFTP) | `omb-images`, `omb-control`, `smbd` |
| `/var/lib/omb-deployment/` | Состояние пакетного развёртывания: `COMMIT`, `PACKAGE`, `PACKAGE_SHA256`, `CHANNEL` | `offline-deploy.sh` |
| `/srv/omb/tftp/` | Корень TFTP и HTTP-ассетов: `ipxeboot/`, `windows/wimboot`, `windows-profiles/`, `windows-answers/`, `windows-driver-wims/` (дедупликация по SHA-256), `windows-driver-configs/`, `ubuntu-sources/` | `omb-agent` |
| `/mnt/omb-smb/<ключ>`, `/mnt/omb-nfs/<ключ>` | Точки монтирования сетевых хранилищ для чтения (`*-upload/<ключ>` для записи) | `omb-agent` |
| `/mnt/omb-iso/profiles/<id>` | Loopback-монтирования ISO Windows и Strelec (`mount -o loop,ro`) | `omb-winiso@` |
| `/var/backups/omb/` | Резервные копии перед офлайн-обновлениями (бинарники, конфиги, дампы БД) | `offline-deploy.sh` |

> [!WARNING]
> Файл `/etc/omb/secrets.key` (32 байта / 64 hex-символа, права `0600 omb-control:omb-control`) шифрует секреты интеграций AD/LDAP, Keycloak и TOTP-ключи 2FA (AES-256-GCM). Обязательно включайте `/etc/omb/` в резервную копию — при утере ключа придётся пересохранить пароли интеграций и сбросить 2FA пользователям.

## Сетевые порты

| Порт и протокол | Служба | Кому нужен доступ |
| --- | --- | --- |
| TCP `8443` | `omb-control` | Сеть управления — веб-интерфейс администраторов и операторов (HTTPS) |
| TCP `8080` | `omb-boot` | Загрузочная PXE-сеть — выдача `menu.ipxe`, ассетов, `boot.wim`, ISO Ubuntu и Autoinstall |
| UDP `67` | `omb-dnsmasq` | Загрузочная PXE-сеть — DHCP-сервер (в режиме Full DHCP) |
| UDP `4011` | `omb-dnsmasq` | Загрузочная PXE-сеть — Proxy DHCP |
| UDP `69` | `omb-dnsmasq` | Загрузочная PXE-сеть — TFTP (`undionly.kpxe`, `ipxe-shim.efi`, `ipxe.efi`, `snponly.efi`) |
| TCP `445` | `smbd` | Загрузочная PXE-сеть для RO-шары `[ombisos]` (ограничено `hosts allow`) и сеть управления для шары `[iso]` (если включена) |
| TCP `2022` (настраивается) | `omb-control` | Сеть управления — встроенный SFTPv3-сервер для загрузки ISO (если включён) |

## Ограничения `omb-agent.service`

Служба `omb-agent` работает под `root`, но изолирована параметром systemd `ProtectSystem=strict` с явным белым списком каталогов для записи (`ReadWritePaths`). При ручном развёртывании каталоги `/etc/omb`, `/etc/omb/winiso`, `/mnt/omb-smb`, `/mnt/omb-smb-upload`, `/mnt/omb-iso/profiles` и подкаталоги `/srv/omb/tftp/windows-*` должны существовать до первого запуска агента. При установке через пакет `.deb` они создаются автоматически.
