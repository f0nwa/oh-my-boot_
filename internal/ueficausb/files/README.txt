PXE Platform: USB stick to enable the Microsoft UEFI CA key
============================================================

What it does
------------
Some laptops (Secured-core and others) ship with the BIOS option for the
"Microsoft UEFI CA" (third-party UEFI CA) key turned off. With Secure Boot on,
such a machine refuses the network boot of the PXE menu. This stick boots with
Secure Boot on, shows the manufacturer, model, serial number, the Secure Boot
state, the option state and whether a BIOS password is set, and enables the
option after you confirm it. Lenovo, Dell and HP are supported. Nothing is
changed without your confirmation. The stick needs no network.

How to write the stick
----------------------
1. Format a USB stick as FAT32 (MBR or GPT partition table).
2. Unpack this archive into the root of the stick, keeping the folders.

How to use it
-------------
1. Insert the stick, turn the machine on and choose the stick in the boot
   menu (F12 on most models). Secure Boot may stay on.
2. Read the screen. If the option is off, type Y and press Enter.
3. If a BIOS administrator (supervisor) password is set, type it and press
   Enter. The keyboard is set to the US layout. You have 2 attempts; the
   screen is cleared after each one. The password is not saved anywhere.
4. After the change the machine restarts in 10 seconds. Then choose the
   network boot in the boot menu.

BitLocker
---------
The stick cannot suspend BitLocker. If the disk has BitLocker and its Windows
must keep working, have the recovery key ready: Windows will ask for it once
after this change.

Two kinds of stick
------------------
"2011": the boot loader signed with the Microsoft Windows Production PCA 2011
key. Boots on almost all machines.
"2023": the boot loader signed with the Windows UEFI CA 2023 key. Use it on
machines where the 2011 loader is already revoked (DBX).
