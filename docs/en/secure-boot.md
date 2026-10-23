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

## If a machine with Secure Boot still does not boot

- **The “Microsoft UEFI CA” key is disabled in the firmware.** Microsoft signs other vendors' loaders (iPXE's shim, Linux shims) with the Microsoft UEFI CA key. Some new laptops, for example Secured-core PCs, trust only the Windows key by default. Turn on an option like “Allow Microsoft 3rd party UEFI CA” in the Secure Boot settings; vendors name it differently.
- **Firmware with empty keys.** Virtual machines sometimes use firmware without Microsoft's keys. In Proxmox, for example, “Pre-Enroll keys” must be ticked when creating the EFI disk.
- **A signature message or “Access denied” right after the loader is fetched** usually means the firmware does not trust the shim; check both points above.

## How to check what the server serves

The signatures can be seen in the files themselves, for example with `strings -a`:

- `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe-shim.efi` contains `Microsoft Corporation UEFI CA 2011` and `iPXE Secure Boot CA`;
- `/srv/pxe/tftp/ipxeboot/x86_64-sb/ipxe.efi` contains `iPXE Secure Boot Automatic Code Signing`;
- `/srv/pxe/tftp/windows/wimboot` is `wimboot v2.9.0` with a signature in the file.

More: [iPXE — UEFI Secure Boot](https://ipxe.org/secboot), [the shim command](https://ipxe.org/cmd/shim).
