# Secure Boot: почему PXE Platform грузится без его отключения

## Коротко

На машине с включённым Secure Boot прошивка запускает только программы, подписанные ключом, которому она доверяет. В каждой такой прошивке с завода есть ключ Microsoft. PXE Platform отдаёт машине цепочку загрузчиков, где каждое звено подписано, а каждое следующее проверяет подпись предыдущего. Поэтому Secure Boot выключать не нужно.

Некоторые другие PXE-решения, например iVentoy, грузят машину своим неподписанным загрузчиком. Прошивка его не пропускает, и Secure Boot приходится выключать или вручную добавлять ключ на каждую машину.

## Цепочка загрузки

Как эстафета: каждый следующий бегун проверяет пропуск у предыдущего.

```
Прошивка UEFI (доверяет ключу Microsoft)
  └─ ipxe-shim.efi        подписан Microsoft (Microsoft Corporation UEFI CA 2011)
       └─ ipxe.efi        подписан ключом проекта iPXE; ключ зашит в shim
            ├─ Windows:  wimboot      подписан ключом iPXE
            │              └─ загрузчик Windows из boot.wim — подписан Microsoft
            └─ Ubuntu:   shim Ubuntu  подписан Microsoft
                           └─ GRUB и ядро — подписаны Canonical
```

1. **Прошивка → shim.** Машина в режиме UEFI получает от сервера по сети `ipxeboot/x86_64-sb/ipxe-shim.efi` (`sb` значит Secure Boot). Это маленький загрузчик-посредник из официальной сборки iPXE, подписанный Microsoft. Прошивка проверяет подпись своим ключом Microsoft и запускает его.
2. **shim → iPXE.** Внутри shim зашит сертификат проекта iPXE («iPXE Secure Boot CA»). shim загружает соседний `ipxe.efi`, проверяет, что он подписан этим ключом, и запускает. Дальше работает iPXE: показывает меню и скачивает файлы установки по HTTP.
3. **iPXE → Windows.** Для установки Windows iPXE запускает wimboot (`windows/wimboot`, версия 2.9.0), тоже подписанный ключом iPXE. wimboot подставляет в память boot.wim и запускает из него загрузчик Windows, подписанный самой Microsoft.
4. **iPXE → Ubuntu.** Пункт Ubuntu использует команду iPXE `shim`: она запускает собственный shim Ubuntu (`ubuntu/bootx64.efi`, подписан Microsoft), а тот проверяет и запускает GRUB и ядро, подписанные Canonical. Если Secure Boot выключен, команда `shim` ничего не делает и не мешает.

Машины в режиме Legacy BIOS получают `ipxeboot/x86_64/undionly.kpxe`. Secure Boot в BIOS нет, подписи там не проверяются.

## Что это значит на практике

- **Загрузчик для UEFI нельзя пересобрать самим.** Работают только готовые подписанные `ipxe-shim.efi` и `ipxe.efi` с ipxe.org. Своя сборка iPXE, например с картинками в меню, будет без подписи, и на машинах с Secure Boot её придётся отключать. Поэтому оформление меню ограничено возможностями официальной сборки.
- **Для Legacy BIOS свою сборку делать можно**: Secure Boot там не участвует.
- **Всё, что запускается из меню, тоже должно быть подписано.** Windows и Ubuntu подписаны. Неподписанный образ (утилита, старый Linux без подписанного shim) на машине с Secure Boot не запустится. Для него Secure Boot придётся выключить на этой машине.
- **Драйверы, которые мы добавляем в WinPE, на загрузку не влияют.** Их проверяет уже Windows, а не прошивка.

## Если машина не загружается

### Как это выглядит

На экране машины после строк про скачивание загрузчика:

```
NBP file downloaded successfully.
BdsDxe: failed to load Boot0001 "UEFI PXEv4 ..." : Access Denied
```

После этого машина пробует загрузку по IPv6 или со следующего устройства, а наше меню не появляется. На других моделях вместо «Access Denied» пишут «Security Violation» или «Image authorization failed».

В панели на вкладке «Сейчас» DHCP и TFTP зелёные, а шаг iPXE красный с подписью «отклонён прошивкой». На странице машины итог называется «Прошивка отклонила загрузчик». Панель ставит его, если машина скачала `ipxe-shim.efi`, а следующий файл, `ipxe.efi`, за 30 секунд так и не запросила. Запущенный shim запрашивает `ipxe.efi` сразу, поэтому тишина значит, что прошивка его не запустила.

### Почему так

Прошивка с Secure Boot запускает файл, только если его подпись есть в её списке доверенных ключей (db). Наш `ipxe-shim.efi` подписан ключом **Microsoft Corporation UEFI CA 2011**: им Microsoft подписывает загрузчики других разработчиков. Обычные компьютеры доверяют этому ключу с завода. А ноутбуки с пометкой **Secured-core PC** и некоторые корпоративные модели по умолчанию доверяют только ключу самой Windows (Microsoft Windows Production PCA 2011). Для такой прошивки shim чужой, и она отказывает ещё до его запуска.

Положить нужный сертификат в shim или в меню нельзя: прошивка проверяет shim раньше, чем он успевает что-либо сделать. Флешка Ventoy на таком ноутбуке получит тот же отказ, потому что её shim подписан тем же ключом. Пункт «Enroll key» в Ventoy работает уже после запуска shim и в этой ситуации не помогает.

### Что сделать

Подходит любой из трёх способов, по одному разу на каждую машину.

**1. Разрешить ключ Microsoft для сторонних загрузчиков (рекомендуем).** В настройках Secure Boot включите пункт про «3rd party» или «Microsoft UEFI CA». Secure Boot остаётся включённым, а машина начинает доверять подписанным shim: нашему и от Linux. Названия зависят от модели и версии BIOS, примеры:

| Производитель | Где искать |
|---|---|
| Lenovo (ThinkPad, ThinkCentre) | Security → Secure Boot → «Allow Microsoft 3rd Party UEFI CA» → On |
| Dell | Secure Boot → «Enable Microsoft UEFI CA» (на части моделей в разделе Expert Key Management) |
| HP | Advanced → Secure Boot Configuration → «Enable MS UEFI CA key» |
| Microsoft Surface | UEFI → Security → Secure Boot → «Microsoft & 3rd party CA» |

Если такого пункта нет, поищите режим ключей вроде «Factory Keys» или «Restore Factory Keys»: на некоторых моделях стандартный набор уже содержит нужный ключ.

**2. Добавить наш загрузчик в доверенные вручную.** Некоторые BIOS умеют «Enroll EFI Image», «Add Trusted File» или «Append Image Hash». Скопируйте `ipxe-shim.efi` с сервера (`/srv/pxe/tftp/ipxeboot/x86_64-sb/`) на флешку FAT32, выберите его в этом пункте BIOS и сохраните. Прошивка запомнит отпечаток именно этого файла. Если shim на сервере обновится, процедуру придётся повторить. Пункт есть не у всех производителей, и обычно он работает только с флешки или диска, а не по сети.

**3. Отключить Secure Boot.** Самый простой способ, но защита на машине выключается целиком. Подходит для стенда или машины, которую после установки всё равно переводят в общий парк: после установки Secure Boot можно включить обратно, Windows с ним грузится.

### Другие причины

- **Прошивка с пустыми ключами.** У виртуальных машин бывают варианты прошивки без ключей Microsoft. В Proxmox при создании EFI-диска должна стоять галочка «Pre-Enroll keys».
- **Шаг TFTP красный, а не iPXE.** Тогда загрузчик не дошёл до машины, и Secure Boot ни при чём: смотрите сеть и настройки DHCP.

### Как воспроизвести на виртуальной машине

Строгую прошивку «как у Secured-core» можно собрать для тестовой ВМ в Proxmox. Это пригодится, чтобы проверить поведение панели или новую версию загрузчика. На хосте Proxmox:

```
# ВМ без диска с загрузкой только по сети; мост — тот, где работает PXE
qm create 950 --name sb-strict-test --machine q35 --bios ovmf --cores 2 --memory 4096 \
  --net0 virtio,bridge=vmbr0 --boot order=net0 \
  --efidisk0 local-lvm:1,efitype=4m,pre-enrolled-keys=1

# свежий virt-firmware: в пакете Proxmox нет нужных флагов
apt install -y python3-venv
python3 -m venv /root/vf && /root/vf/bin/pip install -U virt-firmware

# ключи «только Windows»: без Microsoft UEFI CA
/root/vf/bin/virt-fw-vars --input /usr/share/pve-edk2-firmware/OVMF_VARS_4M.fd \
  --output /root/sb-windows-only.fd \
  --enroll-microsoft --microsoft-db win11 --microsoft-kek all --sb

# записать на EFI-диск выключенной ВМ и запустить
dd if=/root/sb-windows-only.fd of=$(pvesm path local-lvm:vm-950-disk-0) bs=1M conv=notrunc
qm start 950
```

ВМ получит «Access Denied», как Secured-core ноутбук. Чтобы проверить исправление по способу 1, соберите ключи с `--microsoft-db all` вместо `win11`, снова запишите их на диск той же командой `dd` и запустите ВМ. Меню загрузится при включённом Secure Boot. Проверено 23.10.2026.

## Где проверить, что стоит на сервере

Подписи можно увидеть в самих файлах, например `strings -a`:

- в `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe-shim.efi` есть строки `Microsoft Corporation UEFI CA 2011` и `iPXE Secure Boot CA`;
- в `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe.efi` — `iPXE Secure Boot Automatic Code Signing`;
- `/srv/pxe/tftp/windows/wimboot` — `wimboot v2.9.0` с подписью в файле.

Подробнее: [iPXE — UEFI Secure Boot](https://ipxe.org/secboot), [команда shim](https://ipxe.org/cmd/shim).
