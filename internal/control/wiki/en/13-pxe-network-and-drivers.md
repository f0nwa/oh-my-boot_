---
slug: pxe-network-and-drivers
section: infra
order: 20
title: PXE Network and UEFI Driver Settings
summary: Full DHCP vs Proxy DHCP modes, choosing between ipxe.efi and snponly.efi, MAC/OUI rule precedence, and safe zero-downtime reloads.
keywords: [network, dhcp, proxy dhcp, dnsmasq, snponly, ipxe.efi, mac, oui, sighup, tftp]
---

All PXE network parameters are configured under [System → Network](/network). The form is organized into four sections inside a single card, with a sticky **“Network boot scenario”** panel on the right showing in real time which file clients will receive and which options are active.

## DHCP modes

| Mode | When to use | How it works |
| --- | --- | --- |
| **Full DHCP** (`full`) | Isolated lab or dedicated deployment VLAN with no other DHCP server | `omb-dnsmasq` assigns IP addresses from the configured pool, subnet mask/gateway, and PXE options |
| **Proxy DHCP** (`proxy`) | Corporate network where a router or Windows DHCP already assigns IP addresses | DHCP pool fields are dimmed and disabled; `omb-dnsmasq` replies to PXE requests only (UDP `4011`) |

Before applying changes, OMB opens a confirmation preview at `/network/preview`, highlighting modified rows with an amber marker and a “changed” badge. On apply, `omb-agent` validates the new config with `dnsmasq --test` and rolls back on any error.

## Choosing the UEFI network driver: `ipxe.efi` vs `snponly.efi`

Under UEFI, iPXE can drive the network adapter in two ways:

- **Built-in drivers (`ipxe.efi`)** — classic mode using iPXE's native NIC drivers. Works well on most desktops and VMs.
- **Firmware driver (`snponly.efi`)** — uses the UEFI firmware's own network stack (SNP). Recommended for modern laptops, USB-C docks, and NICs not yet supported by `ipxe.efi`.

### Driver rule precedence

Driver selection works at three levels (the most specific non-empty rule wins):

1. **Individual machine** — configured on `/events/machines/{mac}` or in [System → Machines](/machines).
2. **MAC prefix rule (OUI, 3–5 bytes)** — configured in [System → Machines](/machines) for an entire hardware line or dock family (when multiple prefixes match, the longest prefix wins).
3. **Global default driver** — configured under [System → Network](/network).

> [!NOTE]
> Per-machine and MAC-prefix rules are written to `/var/lib/omb-dhcp/hosts` and applied by sending `SIGHUP` to `omb-dnsmasq` — without restarting the service or dropping active DHCP/TFTP sessions.

## “Reuse firmware network settings” option

The `reuse_firmware_network` checkbox under **iPXE bootloader** enables DHCP sub-option `175.81`: iPXE reuses the IP address already obtained by UEFI firmware during stage 1 instead of running a second DHCP cycle. This speeds up booting and helps Dell laptops after a warm reboot from Windows Setup.

The option takes effect only when both conditions are met (verified live in the “Network boot scenario” panel):

1. **Full DHCP OMB** mode is enabled.
2. The client uses the **firmware (`snponly`)** driver.
