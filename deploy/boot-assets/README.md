# iPXE boot assets

Use the official [iPXE v2.0.0 release](https://github.com/ipxe/ipxe/releases/tag/v2.0.0) for `ipxe.efi` and the [iPXE shim 16.1 release](https://github.com/ipxe/shim/releases/tag/ipxe-16.1) for the Microsoft 2011/2023 dual-signed shim. Both URLs and checksums are pinned:

```text
https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz
SHA-256 01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1
https://github.com/ipxe/shim/releases/download/ipxe-16.1/ipxe-shimx64.efi
SHA-256 5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf
```

The archive checksum was checked on 2026-10-15. The shim checksum matches the release asset digest reported by GitHub and was checked on 2026-10-29. The project does not redistribute these binaries.

Install into an absent destination:

```sh
sudo ./scripts/fetch-ipxe.sh \
  https://github.com/ipxe/ipxe/releases/download/v2.0.0/ipxeboot.tar.gz \
  01a526d4cc791fc30362259c609d6c506cc64a7bdff51b9a5eb788354e17eee1 \
  https://github.com/ipxe/shim/releases/download/ipxe-16.1/ipxe-shimx64.efi \
  5eecca2780bd49c900565e124516a1bd666ec5e012825f34991b6ba1ef2fa6cf \
  /srv/pxe/tftp
```

The script replaces only the archive's `x86_64-sb/shimx64.efi` before publishing the directory. Both `ipxe-shim.efi` and `snponly-shim.efi` remain links to that file. The signed `ipxe.efi` and `snponly.efi` from v2.0.0 are unchanged. `ipxeboot/x86_64/undionly.kpxe` is offered to BIOS clients. The shim loads the matching EFI binary from the same directory; keep the archive layout intact. See the [upstream Secure Boot instructions](https://ipxe.org/secboot). The new shim's signatures and TFTP transfer were verified on the test server; a VM boot with a 2023-only `db` remains to be checked.

For the optional global Debian GRUB test path, stage a separate Debian netboot archive under `debian-installer/amd64` before selecting `grub-test`. The pinned archive, file checksums, menu and OVMF check are in [the GRUB Secure Boot test guide](../../docs/grub-secure-boot-test.md). BIOS and the default iPXE assets stay in place.
