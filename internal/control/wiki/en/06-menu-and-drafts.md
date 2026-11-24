---
slug: menu-and-drafts
section: panel
order: 10
title: Boot Menu, Drafts, and Publishing
summary: How the menu draft model works, iPXE screen preview, folders, local disk timeout, and rolling back to previous versions.
keywords: [menu, draft, publish, rollback, folders, timeout, drag-and-drop, ipxe]
---

Network clients never see half-finished edits. All changes to the menu structure happen in a draft, and clients switch over atomically when you publish.

## How the draft works

When you add a boot item to the menu, rename an entry, reorder rows, or change the timeout, changes are saved in `menu_draft` and `menu_draft_items` in the `omb_control` database.

Whenever the draft differs from the published version, an amber bar appears at the top of every page (except Now):

- active version indicator (`v12 → draft`);
- diff summary: `+N` added, `−N` removed, `~N` modified;
- actions: “View changes”, “Discard changes”, and “Publish menu”.

When you click “Publish menu”, `omb-control` verifies that all included boots are ready (including a strict check of Ubuntu sources), renders `menu.ipxe`, stores a new immutable record in `menu_versions`, and pushes it to `omb-boot` over the Unix socket.

> [!NOTE]
> Editing a boot item under [Library → Boots](/installations) does not automatically update the live menu — publish the updated draft to push changes to clients.

## Organizing the menu on `/menu`

- **Item order:** drag and drop rows or use the move buttons. The table order matches the iPXE screen order exactly.
- **Menu folders:** group boots into folders (such as `Windows`, `Linux`, `Service`) to keep the root screen uncluttered.
- **Item titles:** iPXE text mode only renders ASCII cleanly, so menu item titles use Latin characters.
- **Default item and timeout:** by default, after the timeout expires (for example, 10 s), the machine falls back to the local disk (`Boot local disk`). This protects workstations from accidental reinstallation on reboot.
- **iPXE preview:** the `/menu` page renders an exact preview of the screen operators will see on client machines.

## Version history and rollback

Click “Version history” on [Menu](/menu) to open a modal list of all past publications with author, timestamp, and SHA-256 checksum.

Any past version can be restored with “Rollback”. Before rolling back, OMB verifies that all images, profiles, and answer files referenced by that version still exist on the server.

> [!IMPORTANT]
> Rolling back a menu version restores the `menu.ipxe` script only — it does not recover deleted ISO files, removed drivers, or disconnected network storages.
