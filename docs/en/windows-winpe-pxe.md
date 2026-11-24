# Windows 10: network installation through WinPE in OMB

[Русская версия](../windows-winpe-pxe.md). This guide covers current ISO profiles. Static entries and separate shares from the first trials are retired.

## Preparation

1. Connect storage and find a Windows 10 x64 ISO in the library. Set up Images and the server components using the [storage guide](images-installation.md).
2. Create an ISO profile and choose **Prepare**. Wait until it is ready. The source ISO must remain available; `boot.wim` alone is not enough for installation.
3. Optionally upload a Windows XML under **Library → Answer files**, and INF files with their dependencies under **Library → Drivers**.
4. Create a Windows installation using this profile. Leave the answer file empty for manual Setup or select a checked XML for automation. Select the required INF files and wait for WIM preparation.
5. Add the installation to the menu draft and choose **Publish**. Publishing checks dependencies. Saving a draft alone does not change the client menu.

See the [Control guide](control-installation.md#winpe-driver-catalog) for XML and driver selection. Examples cover [language settings](../../examples/windows-answer-language-only.xml) and [an installation key without disk selection](../../examples/windows-answer-key-no-disk.xml); check their suitability for the specific ISO.

## Boot and diagnostics

iPXE starts WinPE through `wimboot`. The prepared `omb-setup.cmd` connects installation media through `ombisos` using `omb-iso` and starts Setup, passing the selected XML. NAS credentials stay on the server. If the source is unavailable, check WinPE networking, the ISO profile and SMB. If the disk is missing, check the selected storage controller driver.

The Windows 10 profile previously reached Setup and disk selection in SeaBIOS. Repeat that scenario with the current build.

XML can select and erase disks. To check startup only, use a separate test VM and an XML without automatic partitioning. On 1 November 2026 the user confirmed UEFI Secure Boot loading with XML and drivers. This does not confirm completion of installation or every scenario.

## Menu recovery and repeat acceptance

Use **Menu → Versions** to return to a published version while its dependencies remain available. This does not restore deleted ISOs or undo client disk changes. See the [offline release guide](offline-release.md) for package rollback.

The full BIOS, UEFI, Secure Boot, XML and driver test run is deferred until the main development work is complete.
