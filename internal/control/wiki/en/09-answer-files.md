---
slug: answer-files
section: os-scenarios
order: 20
title: Installation Scripts and Answer File Editor
summary: Shared library of Windows and Ubuntu scripts, built-in autounattend.xml generator, install.wim compatibility checks, and manual test records.
keywords: [scripts, answer files, autounattend, unattend, xml, editor, generator, pe.cmd, compatibility]
---

[Library → Installation scripts](/answers) brings together answer files for Windows (`autounattend.xml`) and Ubuntu (`user-data`). Scripts are not locked to a single ISO — one verified answer file can be reused across multiple images.

## Built-in `autounattend.xml` editor for Windows

OMB includes an offline Go port of `cschneegans/unattend-generator` (`internal/unattend`). Open it via “Add script… → Create in editor” on `/answers`:

- **12 configuration sections:** language and keyboard layouts, edition and product key, disk partitioning, user accounts, Windows 11 requirement bypasses (TPM/Secure Boot/RAM if needed), OOBE settings, File Explorer and taskbar tweaks, inbox app removal, Wi-Fi, and custom scripts.
- **3 built-in presets** for a quick start (such as a corporate baseline or lab profile).
- **Importing existing XML:** upload an existing `autounattend.xml` (up to 1 MB). Unknown tags are preserved in the “Custom XML” section, while editor state is stored inside the XML under `<Editor xmlns="urn:omb:unattend-editor" version="1">`.
- **“Wipe disk and partition automatically” mode:** for WinPE network installs, OMB generates a helper `pe.cmd`, maps the profile folder to `I:`, and passes the partition layout to `setup.exe`. These scripts are flagged with an explicit **“Changes partitions”** warning badge in the table so operators do not pick them by mistake.

> [!WARNING]
> An answer file with automatic partitioning destroys all partitions on the target disk without further confirmation. Always check the “Changes partitions” badge and test the scenario on a non-production machine before publishing to the shared menu.

## Compatibility checks with Windows images

When creating or editing a boot under [Library → Boots](/installations), OMB runs a three-level compatibility check of the selected XML against the image's `install.wim` metadata:

1. **Architecture:** CPU match (`amd64`).
2. **Edition and image index:** verifies that the edition name or index specified in the XML (for example, `Windows 11 Pro`) exists in that specific `install.wim`.
3. **OS family and partitioning:** warns about potential conflicts (such as partition-wiping scripts or mismatched language settings).

Creating a boot also uses smart **“OS + Script”** menu title composition that strips duplicated OS prefixes automatically.

## Manual verification tracking

The technical “Ready” status only means the XML is valid and saved. So the team knows which scripts have actually been tested on VMs or physical hardware, `/answers` includes a dedicated **“Check”** column:

- via the `☰` row menu, an operator records a test outcome (“Passed” or “Failed”), optionally linking it to a specific Windows image with a note (laptop model, BIOS version, observations);
- full check history is retained with author and date; the latest entry per `(script, image)` pair determines the displayed status.
