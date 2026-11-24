---
slug: ubuntu-desktop
section: os-scenarios
order: 40
title: Ubuntu Desktop and Autoinstall
summary: Network booting Ubuntu Desktop amd64 over HTTP with Secure Boot support and the built-in NoCloud Autoinstall editor.
keywords: [ubuntu, linux, autoinstall, nocloud, user-data, meta-data, casper, shim]
---

OMB supports multiple Ubuntu Desktop amd64 images side by side — both in interactive Live/Install mode and fully automated via NoCloud Autoinstall.

## Preparing an Ubuntu ISO image

1. Upload an Ubuntu Desktop ISO to any storage and run “Inspect ISO” on [Library → ISO Images](/images).
2. When you create a boot, `omb-agent` extracts the following into `/srv/omb/tftp/ubuntu-sources/<id>/`:
   - the `vmlinuz` kernel and `initrd` initial ramdisk;
   - the signed Ubuntu `shim` (`ubuntu/bootx64.efi`) — required to pass Secure Boot when chaining from iPXE to the Linux kernel;
   - the list of available application presets for the editor.

To keep `/installations` and `/installations/new` fast, Ubuntu source readiness is cached in memory for 30 s (protected by `singleflight`), while publishing the menu or creating a new boot always performs a strict live file check.

> [!IMPORTANT]
> `casper` downloads the entire Ubuntu ISO over HTTP into client RAM. Modern Ubuntu Desktop images (~6.5 GB) require **more than 4 GB of RAM** on the client (verified on 16 GB RAM machines). Insufficient RAM causes the boot to fail at `initramfs`.

## Ubuntu Autoinstall editor

On [Library → Installation scripts](/answers), click “Add script… → Ubuntu: Create in editor”. The built-in editor generates valid NoCloud `user-data` (validated against JSON Schema) and `meta-data`, hashing passwords with `yescrypt`.

The editor is organized into **8 sections**:

1. **General:** language, keyboard layout, time zone, hostname.
2. **User account:** username, password (`yescrypt` hash), SSH keys, and enabling OpenSSH.
3. **Storage layout (3 modes):**
   - manual partitioning in the installer;
   - automatic wipe of the selected disk;
   - automatic wipe matched strictly by disk serial number (protects neighboring drives from being erased).
4. **Network and package mirrors:** local corporate `apt` mirror or **offline (no network)** installation mode.
5. **Packages and updates:** up to 128 additional packages.
6. **Application sets and environment.**
7. **Custom post-install script** (`/bin/sh -eu`, up to 16 KiB).
8. **YAML preview** and downloading `user-data` before saving to the library.

The saved file is stored in `omb_control` and served to clients over HTTP via `/assets/ubuntu-autoinstall/...`. As with Windows, manual test outcomes for Ubuntu scripts can be recorded in the “Check” column on `/answers`.
