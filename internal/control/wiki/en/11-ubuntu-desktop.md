---
slug: ubuntu-desktop
section: os-scenarios
order: 40
title: Ubuntu Desktop and Autoinstall
summary: Ubuntu Desktop network boot with Secure Boot, the NoCloud Autoinstall editor, and YAML uploads.
keywords: [ubuntu, linux, autoinstall, nocloud, user-data, meta-data, casper, shim, upload YAML]
---

OMB supports multiple Ubuntu Desktop amd64 images side by side — both in interactive Live/Install mode and fully automated via NoCloud Autoinstall.

## Preparing an Ubuntu ISO image

1. Upload an Ubuntu Desktop ISO to a writable storage and run “Check ISO” on [Library → Images](/images).
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

To edit a saved script, click its title in the library. **“Save”** updates the current script. **“Save as new”** creates a separate one. If the password field is empty, the editor reuses the existing password hash. Republish the draft menu to serve the updated script to clients.

## Uploading an existing YAML file

On [Installation scripts](/answers), choose “Add script…” → Ubuntu → “Upload YAML…”. Use a UTF-8 NoCloud `user-data` file up to 64 KiB. It must contain one YAML document with a `#cloud-config` header and an `autoinstall` section with `version: 1`. OMB checks the structure, rejects duplicates, and identifies the disk layout mode when it recognizes it in the YAML.

Click the title to view, copy, or download the YAML. You can rename the script from its row menu. To replace the contents, upload the revised YAML as a separate script and select it in the relevant boot entry.

Select a saved script in an Ubuntu boot entry and publish the menu. OMB serves `user-data` and `meta-data` to clients over HTTP via `/assets/ubuntu-autoinstall/...`. Record the outcome of a test installation in the script table's “Check” column.
