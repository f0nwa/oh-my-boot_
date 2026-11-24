---
slug: quick-start
section: start
order: 20
title: "Quick Start: From Install to First Boot"
summary: Step-by-step path to bring up a new OMB server — from checking services to booting the first client over the network.
keywords: [quick start, setup, first run, publish, checklist]
---

This guide covers the minimal sequence of steps on an installed OMB server to bring your first client machine to the boot menu and start an installation.

## Step 1. Check services and system time

Open [System → Overview](/system) and verify that all services are running and the server date and time zone are correct. Incorrect time breaks TLS certificate validation, 2FA (TOTP), and domain authentication.

If needed, verify the services from the server shell:

```bash
systemctl is-active omb-control omb-boot omb-images omb-agent omb-dnsmasq postgresql smbd
```

All seven services must return `active`.

## Step 2. Configure the PXE network

Go to [System → Network](/network) and fill in the four sections:

1. **Network and DHCP:** select the boot network interface, server IP address, and mode — **Full DHCP** (in an isolated VLAN where OMB assigns IP addresses) or **Proxy DHCP** (when an external corporate DHCP server already assigns IPs).
2. **iPXE bootloader:** choose the default UEFI driver — `ipxe.efi` (built-in drivers) or `snponly.efi` (firmware driver, useful for USB-C docks and newer NICs).
3. **WinPE (Windows Setup):** leave the defaults if unsure (90 s network wait timeout, 2 s retry pause).
4. **Endpoints:** verify the menu URL (`http://<IP>:8080/boot/menu.ipxe`) and TFTP directory (`/srv/omb/tftp`).

Click “Save”, review the diff table on the confirmation screen, and apply the settings.

## Step 3. Add ISO images

Open [System → Storages](/storages). The local storage `/var/lib/omb-images/iso` is already connected. Connect SMB or NFS network storages if needed, or enable the local file server (SMB `[iso]` / SFTP `:2022`).

You can also upload an image directly in the browser under [Library → ISO Images](/images). Once the file appears, open its `☰` row menu and run “Inspect ISO” so OMB detects the image type (Windows, Ubuntu, or Strelec) and reads the `install.wim` edition indexes.

## Step 4. Create a boot and publish the menu

1. If needed, prepare an answer file under [Library → Installation scripts](/answers) and upload NIC or storage controller drivers under [Library → Drivers](/drivers).
2. Open [Library → Boots](/installations) and click “New boot”: choose an image, optionally attach an installation script and drivers, check the generated menu item title, and save.
3. Add the boot to the menu draft, open [Menu](/menu), arrange the item order, check the iPXE screen preview, and click “Publish menu”.

> [!IMPORTANT]
> Until you click “Publish menu”, an amber draft bar stays at the top of the panel and clients continue receiving the previous published menu version.

## Step 5. Test a client boot

Boot a client machine over the network (PXE) and open [Now](/). You will see each stage in real time: DHCP request, TFTP download of the bootloader (`undionly.kpxe` for BIOS or `ipxe-shim.efi` for UEFI), loading `menu.ipxe`, and the selected installation item.

If a UEFI Secure Boot client downloads `ipxe-shim.efi` but never reaches `ipxe.efi` and the Now screen shows “Firmware rejected bootloader”, see [Secure Boot, iPXE shim 16.1, and UEFI CA Keys](/wiki/secure-boot).
