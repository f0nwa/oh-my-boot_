# Secure Boot: подготовка ноутбуков в домене через SCCM

Инструкция для ноутбуков Lenovo, Dell и HP, которые на PXE-загрузке получают «Access Denied». В панели у таких машин шаг iPXE красный: «Прошивка отклонила загрузчик». Почему так происходит, описано в [secure-boot.md](secure-boot.md#если-машина-не-загружается). Если коротко: прошивка может доверять только ключам Windows и не доверять сторонним Microsoft UEFI CA 2011 или 2023, которыми подписан наш загрузчик.

Скрипт `scripts/windows/Enable-MicrosoftUefiCA.ps1` включает в BIOS пункт «разрешить ключ Microsoft для сторонних загрузчиков» прямо из Windows, без захода в BIOS. Secure Boot при этом остаётся включённым.

## Что делает скрипт

1. Проверяет, включён ли Secure Boot и есть ли в списке доверенных (db) хотя бы один из сторонних ключей: «Microsoft Corporation UEFI CA 2011» или «Microsoft UEFI CA 2023». Если Secure Boot выключен или любой из ключей уже есть, ничего не меняет.
2. По производителю находит нужный пункт BIOS через встроенный WMI. Ставить утилиты производителя не нужно.

   | Производитель | Пункт BIOS | Имя в WMI |
   |---|---|---|
   | Lenovo ThinkPad | Allow Microsoft 3rd Party UEFI CA | `Allow3rdPartyUEFICA` = `Enable` |
   | Lenovo ThinkCentre | Allow Microsoft 3rd Party UEFI CA | `AllowMicrosoft3rdPartyUEFICA` = `Enabled` |
   | Dell (модели после 2018 года) | Enable Microsoft UEFI CA | `MSUefiCA` = `Enabled` |
   | HP | Enable MS UEFI CA key | `Enable MS UEFI CA key` = `Yes` |

3. Включает пункт. Если на BIOS стоит пароль администратора (супервизора), передаёт его. Пароль не попадает ни в лог, ни в вывод.
4. Приостанавливает BitLocker на одну перезагрузку. При следующем старте прошивка меняет список ключей Secure Boot, и без этой паузы BitLocker попросил бы ключ восстановления.

Изменение вступает в силу после перезагрузки. Лог пишется в `C:\Windows\Temp\OMB-MicrosoftUefiCA.log`.

### Коды выхода

| Код | Что значит | Что делать |
|---|---|---|
| 0 | Ничего не нужно: Secure Boot выключен или один из ключей уже есть | — |
| 3010 | Пункт включён (или был включён раньше), нужна перезагрузка | Перезагрузить. SCCM понимает этот код сам |
| 2 | У модели нет такого пункта или нет WMI-интерфейса производителя | Старые Dell: Dell Command \| Configure, параметр `--MSUefiCA`. Иначе — вручную в BIOS |
| 3 | Нужен пароль BIOS, или переданный пароль не подошёл | Проверить пароль для этого серийного номера |
| 4 | Не Lenovo, Dell или HP | Вручную в BIOS |
| 1 | Непредвиденная ошибка | Смотреть лог |

## Пароли BIOS

Пароли на машинах разные, поэтому скрипт берёт их из CSV-файла по серийному номеру BIOS:

```
Serial,Password
PF3ABC12,пароль-машины-1
5CG1234XYZ,пароль-машины-2
```

Серийный номер машины можно узнать командой `(Get-CimInstance Win32_BIOS).SerialNumber`. В SCCM он есть в инвентаризации (Hardware Inventory → PC BIOS → Serial Number), так что список можно выгрузить отчётом.

Если серийного номера нет в файле, скрипт берёт пароль из параметра `-Password`, если он задан, иначе пробует без пароля. Каждый пароль пробуется один раз: при нескольких неверных попытках подряд BIOS может заблокировать ввод до перезагрузки.

**Осторожно с файлом паролей.** Содержимое пакета SCCM кэшируется на клиенте в `C:\Windows\ccmcache`, и его могут прочитать локальные администраторы. Поэтому:
- для каждой волны делайте отдельный пакет, а в CSV оставляйте только машины этой волны;
- после раскатки удалите пакет с точек распространения, и клиентский кэш со временем очистится.

## Проверка на одной машине

Перед раскаткой проверьте по одной машине каждой модели. Имена настроек взяты из документации производителей, на реальном железе скрипт ещё не запускался.

Из PowerShell от имени администратора:

```
# что будет сделано, без изменений
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -CheckOnly -Password '<пароль>'
# сделать
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -Password '<пароль>'
```

После перезагрузки проверьте, что в `db` есть хотя бы один из сторонних CA:

```
[Text.Encoding]::ASCII.GetString((Get-SecureBootUEFI db).Bytes) -match 'Microsoft Corporation UEFI CA 2011|Microsoft UEFI CA 2023'
```

Если команда вывела `True`, в `db` есть сторонний CA, которому соответствует одна из подписей нового shim. Успешную загрузку в меню проверьте отдельно на конкретном ноутбуке.

## Раскатка через SCCM

1. **Пакет.** Положите в папку на сервере-источнике `Enable-MicrosoftUefiCA.ps1` и `bios-passwords.csv`. Создайте пакет (Software Library → Packages) с этой папкой в качестве источника и разошлите его на точки распространения.
2. **Программа пакета:**
   - Command line:
     ```
     powershell.exe -NoProfile -ExecutionPolicy Bypass -File Enable-MicrosoftUefiCA.ps1 -PasswordFile bios-passwords.csv
     ```
   - Run: Hidden. Program can run: Whether or not a user is logged on. Run mode: Run with administrative rights.
   - After running: Configuration Manager restarts computer. Можно выбрать «No action required», если перезагрузку лучше оставить пользователю. Изменение тогда применится при его следующей перезагрузке.
3. **Коллекция.** Включите в неё только ноутбуки, которые не грузятся по сети: модели Secured-core или конкретные машины, которые панель пометила «Прошивка отклонила загрузчик».
4. **Развёртывание.** Сделайте его Required с окном обслуживания. Пользователь получит обычное уведомление SCCM о перезагрузке.
5. **Результат.** В Monitoring → Deployments видны коды выхода по каждой машине. 3010 и 0 — успех. 2, 3 и 4 расшифрованы в таблице выше. Подробности на самой машине — в `C:\Windows\Temp\OMB-MicrosoftUefiCA.log`.

Только проверить, без изменений (например, чтобы оценить парк заранее), можно тем же пакетом с отдельной программой и параметром `-CheckOnly`.

## Отчёт по парку: `-Report`

`-Report` ничего не меняет и не пишет в лог, а выводит одну строку CSV: `serial,manufacturer,model,secureboot,option,biospassword`, например `PF1ABC,LENOVO,20XW,on,disabled,yes`. Значения: `secureboot` — `on`/`off`; `option` — `enabled`/`disabled`/`missing`/`unknown`; `biospassword` — `yes`/`no`/`unknown` (пароль администратора BIOS по WMI производителя). Код выхода 0. Подходит для скрипта ManageEngine или SCCM: собрать строки со всех машин и увидеть, где ключ уже включён, а где для включения понадобится пароль.

```
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Enable-MicrosoftUefiCA.ps1 -Report
```

## Тесты скрипта

Логика скрипта покрыта тестами Pester с подменой WMI. Они проверяют, какие вызовы и с какими параметрами получает каждый производитель, и что пароль не попадает в лог:

```
Invoke-Pester ./scripts/windows
```

Источники имён настроек: [Lenovo CDRT — ThinkPad Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkpad/secureboot/), [Lenovo CDRT — ThinkCentre Secure Boot](https://docs.lenovocdrt.com/ref/bios/settings/thinkcentre/secure_boot/), [Dell Command | Configure — --MSUefiCA](https://www.dell.com/support/manuals/en-us/command-configure/dcc_5.x_ref_guide/-msuefica?guid=guid-a6452e46-e9b6-4ca3-a88d-8048a650dc7c&lang=en-us), [Dell BIOS WMI](https://www.configjon.com/dell-bios-settings-management-wmi/), [HP — пункты Secure Boot в BCU](https://h30434.www3.hp.com/t5/Business-PCs-Workstations-and-Point-of-Sale-Systems/BIOS-Settings-using-BiosConfigUtility64/td-p/9688689).
