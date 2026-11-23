---
slug: services-and-databases
section: architecture
order: 10
title: Службы, Unix-сокеты и базы данных
summary: "Как устроен OMB внутри: шесть systemd-юнитов, разделение привилегий, локальные Unix-сокеты и три изолированные базы PostgreSQL."
keywords: [архитектура, systemd, postgresql, сокеты, omb-control, omb-boot, omb-images, omb-agent, dnsmasq]
---

OMB работает на одном сервере Ubuntu Server без контейнеров и без внешних брокеров. Система разделена на службы с минимально необходимыми правами, чтобы веб-интерфейс никогда не выполнял привилегированные операции напрямую.

## Шесть служб systemd

| Служба | Учётная запись | Точка входа | Зона ответственности |
| --- | --- | --- | --- |
| `omb-control.service` | `omb-control` | HTTPS `:8443`, SFTP `:2022` | Веб-панель, пользователи, 2FA, AD/LDAP, Keycloak, черновик меню, аудит, эта Wiki |
| `omb-boot.service` | `omb-boot` | HTTP `:8080` | Отдача `menu.ipxe`, ассетов и Autoinstall, журнал загрузок, режим обслуживания |
| `omb-images.service` | `omb-images` | Unix-сокет | Каталог ISO, авторескан, потоковая загрузка (до 32 ГиБ) и миграция ISO |
| `omb-agent.service` | `root` | Unix-сокет | Единственная привилегированная служба: `dnsmasq`, SMB/NFS, сборка `boot.wim`, Samba, NTP |
| `omb-dnsmasq.service` | `dnsmasq` | UDP `67`, `69`, `4011` | Full DHCP или Proxy DHCP, раздача TFTP и правила драйверов по MAC |
| `omb-winiso@.service` | `root` (oneshot) | Запускается агентом | Подготовка и `mount -o loop,ro` образов Windows/Strelec в `/mnt/omb-iso/profiles/<id>` |

Кроме шести юнитов OMB на сервере работают системные `postgresql` и `smbd` (Samba раздаёт загрузочную шару `[ombisos]` и, если включено, шару `[iso]`).

> [!IMPORTANT]
> Остановка `omb-control` не прерывает сетевую загрузку: за выдачу опубликованного меню и образов клиентам отвечают `omb-boot`, `omb-dnsmasq` и `smbd`.

## Внутренняя связь через Unix-сокеты

Службы общаются по HTTP поверх локальных Unix-сокетов без открытия лишних TCP-портов:

| Сокет | Владелец | Кто подключается и зачем |
| --- | --- | --- |
| `/run/omb/agent.sock` | `omb-agent` | `omb-control` и `omb-images` — применение сети, монтирование SMB/NFS, сборка WinPE, время/NTP, метрики сессий |
| `/run/omb-boot/control.sock` | `omb-boot` | `omb-control` — публикация и откат `menu.ipxe`, чтение журнала HTTP-загрузок, управление режимом обслуживания |
| `/run/omb-images/images.sock` | `omb-images` | `omb-control` — каталог хранилищ и ISO, запуск сканирования, загрузки, удаления и миграции |

## Три изолированные базы PostgreSQL

Все базы подключаются локально через peer-аутентификацию, без паролей в файлах окружения:

1. **`omb_control`** (роль `omb-control`, 33 таблицы) — пользователи, сессии, политика паролей, TOTP 2FA и доверенные браузеры, интеграции AD/LDAP и Keycloak, черновик и версии меню, загрузки, профили Windows и источники Ubuntu, файлы ответов и ручные проверки, драйверы WinPE, книга машин и правила префиксов MAC, блокировки обслуживания, системные события, настройки файлового сервера и аудит.
2. **`omb_images`** (роль `omb-images`, 3 таблицы) — подключённые хранилища (`storages`), найденные файлы ISO (`images`) и расписание фоновых задач (`catalog_settings`).
3. **`omb_boot`** (роль `omb-boot`, 1 таблица) — журнал сетевых запросов клиентов (`boot_events`).

## Утилиты командной строки

Помимо сетевых служб в поставку входят консольные утилиты в `/usr/local/bin/`:

- `omb-controlctl` — создание первого администратора (`create-admin`), сброс пароля (`reset-password`) и сброс 2FA (`reset-2fa`).
- `omb-agentctl`, `omb-imagesctl`, `omb-winiso-prepare` — служебные инструменты диагностики и подготовки профилей.

## Быстрая проверка состояния

```bash
systemctl is-active omb-control omb-boot omb-images omb-agent omb-dnsmasq postgresql smbd
ss -xl | grep /run/omb
```
