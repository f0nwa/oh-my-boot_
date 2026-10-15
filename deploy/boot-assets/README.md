# iPXE boot assets

Use the official [iPXE v2.0.0 release](https://github.com/ipxe/ipxe/releases/tag/v2.0.0). The archive URL is pinned:

```text
https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz
SHA-256 01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1
```

The checksum above matches the release asset digest reported by GitHub and a local download checked on 2026-10-15. The project does not redistribute these binaries.

Install into an absent destination:

```sh
sudo ./scripts/fetch-ipxe.sh \
  https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  /srv/pxe/tftp
```

`ipxeboot/x86_64/undionly.kpxe` is offered to BIOS clients. `ipxeboot/x86_64-sb/ipxe-shim.efi` is offered to UEFI x64 clients, including those with Secure Boot. The shim loads `ipxe.efi` from the same directory; keep the archive layout intact. See the [upstream Secure Boot instructions](https://ipxe.org/secboot). Firmware and actual chainloading still need testing on the supplied Ubuntu VM.
