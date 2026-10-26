# Secure Boot: why PXE Platform boots without turning it off

## In short

With Secure Boot on, a machine's firmware runs only programs signed with a key it trusts, and every such firmware ships with Microsoft's key. PXE Platform hands the machine a chain of loaders in which every link is signed and checks the signature of the next. So Secure Boot can stay on.

Some other PXE tools, such as iVentoy, boot the machine with their own unsigned loader. The firmware refuses it, so Secure Boot has to be turned off or a key added by hand on each machine.

## The boot chain

Think of a relay: each runner checks the next runner's pass.

```
UEFI firmware (trusts Microsoft's key)
  └─ ipxe-shim.efi        signed by Microsoft (Microsoft Corporation UEFI CA 2011)
       └─ ipxe.efi        signed with the iPXE project key, built into the shim
            ├─ Windows:  wimboot       signed with the iPXE key
            │              └─ Windows boot loader from boot.wim, signed by Microsoft
            └─ Ubuntu:   Ubuntu's shim  signed by Microsoft
                           └─ GRUB and kernel, signed by Canonical
```

1. **Firmware → shim.** A UEFI machine gets `ipxeboot/x86_64-sb/ipxe-shim.efi` from the server over the network (`sb` stands for Secure Boot). It is a small intermediate loader from the official iPXE build, signed by Microsoft. The firmware checks the signature with its Microsoft key and runs it.
2. **shim → iPXE.** The shim carries the iPXE project certificate (“iPXE Secure Boot CA”). It loads the `ipxe.efi` next to it, checks that it is signed with that key and runs it. From here iPXE works: it shows the menu and downloads the installation files over HTTP.
3. **iPXE → Windows.** To install Windows, iPXE runs wimboot (`windows/wimboot`, version 2.9.0), also signed with the iPXE key. wimboot places boot.wim in memory and starts the Windows boot loader from it, signed by Microsoft itself.
4. **iPXE → Ubuntu.** The Ubuntu item uses iPXE's `shim` command: it runs Ubuntu's own shim (`ubuntu/bootx64.efi`, signed by Microsoft), which checks and runs GRUB and the kernel signed by Canonical. With Secure Boot off, the `shim` command does nothing and does no harm.

Legacy BIOS machines get `ipxeboot/x86_64/undionly.kpxe`. BIOS has no Secure Boot and checks no signatures.

## What this means in practice

- **The UEFI loader cannot be rebuilt by us.** Only the prebuilt signed `ipxe-shim.efi` and `ipxe.efi` from ipxe.org work. A custom iPXE build (for example with pictures in the menu) would be unsigned, and Secure Boot would have to be turned off on the machines. That is why the menu's look is limited to what the official build can do.
- **For Legacy BIOS a custom build is fine**, since Secure Boot is not involved.
- **Everything started from the menu must be signed too.** Windows and Ubuntu are. An unsigned image (a utility, an old Linux without a signed shim) will not start on a machine with Secure Boot; for it, Secure Boot has to be turned off on that machine.
- **Drivers we add to WinPE do not affect booting.** Windows checks them, not the firmware.

## If a machine does not boot

### What it looks like

On the machine's screen, after the lines about downloading the loader:

```
NBP file downloaded successfully.
BdsDxe: failed to load Boot0001 "UEFI PXEv4 ..." : Access Denied
```

The machine then tries IPv6 or the next boot device, and our menu never appears. Other models say “Security Violation” or “Image authorization failed” instead of “Access Denied”.

In the panel, on the “Now” tab, DHCP and TFTP are green and the iPXE step is red with the note “rejected by firmware”. On the machine's page the outcome is “Firmware rejected the loader”. The panel shows this when the machine downloaded `ipxe-shim.efi` but did not ask for the next file, `ipxe.efi`, within 30 seconds. A shim that runs asks for `ipxe.efi` at once, so silence means the firmware did not start it.

### Why

With Secure Boot on, the firmware starts a file only if its signature is in the firmware's list of trusted keys (db). Our `ipxe-shim.efi` is signed with **Microsoft Corporation UEFI CA 2011**, the key Microsoft uses to sign other vendors' loaders. Ordinary computers trust it out of the box. **Secured-core PC** laptops and some business models trust only Windows' own key (Microsoft Windows Production PCA 2011) by default. To such firmware the shim is a stranger, and it refuses before the shim even starts.

Putting the right certificate into the shim or the menu cannot help: the firmware checks the shim before it can do anything. A Ventoy USB stick gets the same refusal on such a laptop, because its shim is signed with the same key. Ventoy's “Enroll key” works only after the shim has started, so it does not help here.

### What to do

Any one of the three, once per machine.

**1. Allow Microsoft's key for third-party loaders (recommended).** In the Secure Boot settings turn on the option about “3rd party” or “Microsoft UEFI CA”. Secure Boot stays on, and the machine trusts signed shims: ours and Linux ones. Names depend on the model and BIOS version; examples:

| Vendor | Where to look |
|---|---|
| Lenovo (ThinkPad, ThinkCentre) | Security → Secure Boot → “Allow Microsoft 3rd Party UEFI CA” → On |
| Dell | Secure Boot → “Enable Microsoft UEFI CA” (on some models under Expert Key Management) |
| HP | Advanced → Secure Boot Configuration → “Enable MS UEFI CA key” |
| Microsoft Surface | UEFI → Security → Secure Boot → “Microsoft & 3rd party CA” |

For domain laptops this option can be turned on from Windows with a script through SCCM, without entering the BIOS: see [secure-boot-sccm.md](secure-boot-sccm.md).

For machines outside the domain, or with a foreign or broken Windows, use the USB stick from the admin page: it boots with Secure Boot on and turns this option on after you confirm (see [below](#usb-stick-for-machines-outside-the-domain)).

If there is no such option, look for a key mode like “Factory Keys” or “Restore Factory Keys”: on some models the standard set already contains the key.

**2. Add our loader to the trusted list by hand.** Some BIOSes offer “Enroll EFI Image”, “Add Trusted File” or “Append Image Hash”. Copy `ipxe-shim.efi` from the server (`/srv/pxe/tftp/ipxeboot/x86_64-sb/`) to a FAT32 USB stick, pick it in that BIOS option and save. The firmware remembers the fingerprint of exactly this file; if the shim on the server is updated, repeat this. Not every vendor has the option, and it usually works only from a USB stick or disk, not over the network.

**3. Turn Secure Boot off.** The simplest, but it turns the protection off completely. Fine for a test bench or a machine that joins the regular fleet after installation: Secure Boot can be turned back on afterwards, and Windows boots with it.

### USB stick for machines outside the domain

**Library → ISO images**, next to a prepared profile: **USB stick: enable the UEFI CA key**. The admin page builds `pxe-uefica-<profile>-<2011|2023>.zip` from that ISO's WinPE (about 600 MB; the first build takes up to a minute, later downloads come from a cache). Unpack it into the root of a FAT32 USB stick (MBR or GPT) and boot the machine from it with the boot menu (F12). Secure Boot may stay on: WinPE is signed with the Windows key. All roles can download it; the audit log records `uefica-usb.download`.

On screen (in English, with the keyboard switched to US):

- manufacturer, model, serial number, Secure Boot, the option state, whether a BIOS password is set;
- if the option is off: a BitLocker warning and the question "Y or N, then Enter";
- if a BIOS administrator password is set (or the BIOS refused without one): the password prompt, at most two attempts, the screen is cleared after each; the password is not stored anywhere;
- after a change the machine restarts in 10 seconds; otherwise "R, Enter" restarts and a plain Enter opens a command prompt.

| Code | Meaning |
|---|---|
| 20 | option turned on, restart (not 0 on purpose: a crashed script may leave 0, which must not restart the machine) |
| 10 | already on, or Secure Boot not in use (BIOS/CSM mode) |
| 2 | not Lenovo/Dell/HP, or the model has no such option |
| 3 | the BIOS did not accept the password |
| 1 | WMI error or a script failure; any other code is treated as an error too |

The stick's boot loader is chosen at download: **PCA 2011** is the ISO's loader and fits almost all machines; **UEFI CA 2023** is `bootmgfw_EX.efi` from `boot.wim` (as Microsoft's `Make2023BootableMedia.ps1` does), for machines where the 2011 loader is already revoked. One stick cannot serve both: the firmware starts only `efi/boot/bootx64.efi`, and Microsoft does not sign the Windows loader with both keys at once.

Limits: BitLocker cannot be suspended from WinPE, so Windows asks for the recovery key once after the change; the logic is VBScript (WinPE has no PowerShell), and if Microsoft removes VBScript from WinPE the fallback is a PowerShell stick built with the ADK; Dell over WMI means models from 2018 on. The vendor logic is the same as in the [SCCM script](secure-boot-sccm.md); WMI stub scenarios are in `internal/ueficausb/testdata/uefica-tests.cmd` (on Windows, or on Linux with Wine: `UEFICA_WINE=1 wine cmd /c uefica-tests.cmd`).

### Other causes

- **Firmware with empty keys.** Virtual machines sometimes use firmware without Microsoft's keys. In Proxmox, “Pre-Enroll keys” must be ticked when creating the EFI disk.
- **The TFTP step is red, not iPXE.** Then the loader never reached the machine and Secure Boot is not involved: check the network and DHCP settings.

### How to reproduce on a virtual machine

Strict “Secured-core” firmware can be built for a test VM in Proxmox, which helps to check the panel or a new loader version. On the Proxmox host:

```
# a diskless VM booting from the network only; use the bridge PXE runs on
qm create 950 --name sb-strict-test --machine q35 --bios ovmf --cores 2 --memory 4096 \
  --net0 virtio,bridge=vmbr0 --boot order=net0 \
  --efidisk0 local-lvm:1,efitype=4m,pre-enrolled-keys=1

# a fresh virt-firmware: the Proxmox package lacks the needed flags
apt install -y python3-venv
python3 -m venv /root/vf && /root/vf/bin/pip install -U virt-firmware

# Windows-only keys: no Microsoft UEFI CA
/root/vf/bin/virt-fw-vars --input /usr/share/pve-edk2-firmware/OVMF_VARS_4M.fd \
  --output /root/sb-windows-only.fd \
  --enroll-microsoft --microsoft-db win11 --microsoft-kek all --sb

# write it to the stopped VM's EFI disk and start it
dd if=/root/sb-windows-only.fd of=$(pvesm path local-lvm:vm-950-disk-0) bs=1M conv=notrunc
qm start 950
```

The VM gets “Access Denied”, like a Secured-core laptop. To check fix 1, build the keys with `--microsoft-db all` instead of `win11`, write them with the same `dd` command and start the VM: the menu loads with Secure Boot on. Verified on 23.10.2026.

## The 2011 and 2023 keys

Microsoft is rotating its Secure Boot keys: **Microsoft UEFI CA 2023** is replacing **Microsoft Corporation UEFI CA 2011**, both for third-party loaders. Firmware does not check key expiry, so loaders signed with the 2011 key keep working wherever that key is in db.

What was checked on 23.10.2026:

- **The server currently serves** the shim from iPXE v2.0.0 (March 2026), with one signature: Microsoft Corporation UEFI CA 2011.
- **The current build on boot.ipxe.org** (`2.0.0+ g70f39`) carries two signatures, one chaining to UEFI CA 2011 and one to Microsoft UEFI CA 2023. That shim boots on firmware with either key. Microsoft dual-signs Linux distributions' shims the same way.
- **We cannot add a signature ourselves.** Microsoft signs with its own private key, and changing a single byte of the shim breaks every signature on it.
- **The new shim does not help Secured-core laptops.** Their db holds only Windows keys (Microsoft Windows Production PCA 2011 and **Windows** UEFI CA 2023), with no third-party key at all, neither 2011 nor 2023. They still need the BIOS option or the [SCCM script](secure-boot-sccm.md).
- The new shim is for the future: machines left with only the 2023 third-party key, meaning firmware shipped without the 2011 key, or the case where Microsoft revokes it.

The move to the dual-signed shim is in the [TODO](../../TODO.md). It needs a tagged iPXE release rather than a nightly build, and a VM test with three key sets: 2023 only, 2011 only, Windows only.

## Why there is no EFI file choice like in iVentoy

iVentoy lets you pick which EFI file to send to machines. Its loader is unsigned, so the file is matched to the firmware. Our chain is signed end to end, and one loader suits every machine, with or without Secure Boot. A global switch could only do harm here: a wrong choice breaks booting for the whole fleet at once.

The choice that does matter is iPXE's network driver. `ipxe.efi` has its own network card drivers, while `snponly.efi` uses the firmware's driver. Some hardware (USB-C docks, USB adapters, some new network cards) works only with the latter. Both are signed and started through the shim, so choosing between them does not affect Secure Boot. It is planned per machine, model or vendor (by MAC prefix): stage 5 of the [machine list plan](../superpowers/plans/2026-10-23-machine-book.md).

## How to check what the server serves

The signatures can be seen in the files themselves, for example with `strings -a`:

- `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe-shim.efi` contains `Microsoft Corporation UEFI CA 2011` and `iPXE Secure Boot CA`;
- `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe.efi` contains `iPXE Secure Boot Automatic Code Signing`;
- `/srv/pxe/tftp/windows/wimboot` is `wimboot v2.9.0` with a signature in the file.

More: [iPXE — UEFI Secure Boot](https://ipxe.org/secboot), [the shim command](https://ipxe.org/cmd/shim).
