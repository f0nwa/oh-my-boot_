# Answer file editor

Under “Library → Installation scripts”, choose “Add script…” → Windows → “Create in editor” to build an `autounattend.xml` for Windows Setup. Click the title of a ready script in the table to open it. The editor uses a Go port of `cschneegans/unattend-generator`. [Upstream library](https://github.com/cschneegans/unattend-generator).

## The page

- **Contents on the left:** 12 sections — language and region, disk and setup, edition and key, name and time, accounts, Explorer and Start, appearance, network and VMs, remove apps, tweaks and security, scripts, custom XML. Marks: “changed” — the section differs from Windows defaults; a number — how many apps, scripts or custom XML elements; “N notes” — the section has notes.
- **One section in the middle.** Above it are presets: “Windows defaults”, “Language and key only — disk by hand”, “Lab: local account, automatic sign-in”. A preset replaces all settings.
- **Tabs on the right:** “Result” — a plain summary; “XML” — the whole file with changed lines highlighted; “Notes” — for example, plain-text passwords or Windows protection turned off. “Wipes the disk” appears when the file partitions the disk itself; its menu item gets “WIPES DISK”.
- **Saving a script.** Enter a title for the library. You can attach the script to any compatible Windows boot entry. When editing, **“Save”** updates the current record and **“Save as new”** creates a separate one. The published menu serves the previous XML until you republish the draft.
- **Download autounattend.xml** — the same file, for USB, ISO or checking.
- The computer name field suggests names from the machine list.
- Generator errors (“line longer than 255 characters”, “at least one administrator is needed” and so on) are shown above the form, and the input stays.
- You can download a file larger than 1 MiB from the editor. The library accepts files up to 1 MiB.
- The page works without JavaScript: moving between sections, presets and “another row” submit the form. With JavaScript, sections switch in place and the summary and XML follow every change.

All roles can open and download scripts. Administrators and operators can save them. The audit log records `windows-answer.create` or `windows-answer.update` with `editor=1`. Computers in the trusted PXE segment can fetch the file, including passwords and keys. “Obscure passwords” encodes passwords in base64.

## Partitioning in network installs

- **“Windows Setup”** — the disk is chosen on the Setup screen; the answer file does not touch it. Works for every install.
- **“Wipe and partition the disk automatically”** — a generated `pe.cmd` replaces Setup in the windowsPE pass: it picks the disk (by number, by size and type, or asks), partitions it (GPT/MBR by firmware, with or without a recovery partition), applies the image with `dism`, makes the system partition bootable and restarts. `pe.cmd` looks for the image as `<drive>:\sources\install.wim`, so for network installs the Windows image boot script (`omb-answer-setup.cmd`) maps the profile folder once more as `I:` and copies the selected script (`omb-answer.xml`) to `X:\autounattend.xml`. Windows profiles prepared previously can be updated by preparing the profile again.
- **“Custom pe.cmd script”** — the same with your own script.

## How files open

- **A file saved by the editor** keeps its settings inside the XML: an `<Editor xmlns="urn:omb:unattend-editor" version="1">` element with JSON in CDATA (base64 when passwords are obscured). Windows Setup skips elements of foreign namespaces. Such a file opens exactly. If it was edited by hand since, the editor warns that saving drops the manual edits.
- **A foreign file** (from the Schneegans site, written by hand, from another generator) is taken apart. Known settings go into the form: language and keyboards, time zone, computer name, edition and key, activation key, Windows 11 check bypass, accounts and automatic sign-in, OOBE screens, the `$OEM$` folder, scripts of Schneegans files. Everything else is kept verbatim under “Custom XML” and merged back into the same component when the file is built: a list item (`RunSynchronous`, `DriverPaths`, accounts and the like) is added unless the same item is there; any other element replaces the form's element of the same name.
- **Self-check:** a setting moves into the form only if rebuilding reproduces its value exactly; otherwise it stays as verbatim XML. So nothing is lost on opening. Only XML comments are dropped, and the editor shows how many there were.
- **Scripts of Schneegans files** (`Specialize.ps1`, `FirstLogon.ps1`, `UserOnce.ps1`, `DefaultUser.ps1`) run in the same phases from `C:\Windows\Setup\Scripts\Imported\`; their helper files stay as they were.
- UTF-16 files with a BOM are read.

A banner above the form lists what went into the form and what stays as custom XML.

## Limits

- The original's final XSD check of the XML is not ported; the port only checks that the XML is well-formed.
- Parse errors of custom XML (Wi-Fi profile, taskbar, Start tiles, AppLocker) come from Go, not .NET: the message starts the same, the reason is worded differently.
- Start pins JSON is accepted as by the original (cut-off text is fine), but without comments and single quotes.
- Custom components are added in row order; on the site, the order of several custom components of one pass is random.

Details: [internal/unattend/UPSTREAM.md](../../internal/unattend/UPSTREAM.md) (in Russian).

## Shared answer-file lists

Library → Installation scripts has two tables: **Windows** and **Ubuntu**. Both have the same ten columns: name, designed for, added by, created, target disk / action, usage, state, checks by image, SHA-256 and actions.

“Added by” identifies the OMB account that created or uploaded the file, not necessarily the author of an external XML. Missing history is shown as “Unknown”. Creation dates include the time zone. “Designed for” shows extracted Windows script target chips (architecture, edition/index, language, partitioning, Win11 bypasses, key) or “Ubuntu Desktop” for Ubuntu; scripts are not hard-bound to a single ISO. When selecting a script in New boot (unified Installation script step starting with “No script — default”, capped at 5 items with search enabled when > 5 items), OMB runs three-layer compatibility checks against the selected Windows image: hard blocking mismatches (architecture, WIM index/edition), confirmable warnings (Win11 bypasses on Windows 10, language mismatch, MBR partitioning), and per-image manual verification status.

“Ready” means the file has been saved and prepared for use in OMB. It does not confirm a successful OS installation. The state tooltip describes the checks performed; the SHA-256 tooltip contains the complete digest.

Usage shows saved downloads with links and a separate count of published menu entries. Published entries retain their published component bindings when a saved download changes. Unavailable reference data is shown as unknown usage rather than zero.

Windows disk descriptions come from unchanged editor settings or conservative XML analysis. Arbitrary commands are shown as script-defined actions with an unknown target. Finding no automatic partitioning does not guarantee data preservation. Ubuntu shows manual selection, installer-selected disk or an exact serial number. Row actions are grouped in the menu on the right and respect the account's role.

## License

unattend-generator is MIT licensed, © Christoph Schneegans. License text: [internal/unattend/LICENSE-unattend-generator.txt](../../internal/unattend/LICENSE-unattend-generator.txt). The original's resources (`internal/unattend/resource/`: PowerShell and VBScript scripts, lookup tables) are copied unchanged.

## Updating the port from upstream

The port is checked against the original with golden files: `internal/unattend/testdata/golden/*.json` are settings, `*.xml` or `*.err` what the original C# library made of them. `go test ./internal/unattend/` compares the port's output byte for byte. Building the project needs no .NET; only regenerating golden files does (`apt-get install dotnet-sdk-8.0`).

1. Run `scripts/unattend-update.sh <commit>`. The script:
   - fetches upstream at that commit into `tools/unattend-golden/upstream/`;
   - copies `resource/` to `internal/unattend/resource/`;
   - writes the new commit into `generator.go` (`UpstreamCommit`), `UPSTREAM.md` and `tools/unattend-golden/fetch.sh`;
   - regenerates the golden files with the new original (`scripts/unattend-golden.sh`);
   - lists the changed C# files and runs the tests.
2. If tests fail, port the changes from `git -C tools/unattend-golden/upstream diff <old> <new> -- modifier/ Main.cs`: modifier `X.cs` goes to `internal/unattend/modifiers_*.go` (functions are named after the C# classes), the settings model to `config.go`. The test prints the first line that differs.
3. Add new upstream settings to the form (`templates/answer_editor.html`, translations in `internal/control/i18n_editor.go`) and, if foreign files carry them, to the import (`importer.go`).
4. Add golden files for new settings: JSON in `testdata/golden/`, then `scripts/unattend-golden.sh testdata/golden/<name>.json`.
5. Update the date in `UPSTREAM.md`, run `go test ./...`, commit.

A dry run — the script on the current commit — changes no golden file or resource and the tests pass. On 24.10.2026 upstream had no commits after `27237f8`.


## Manual installation checks

The Checks by image column in both answer-file tables records operators' test results per image (up to three recent entries inline plus a button for remaining history). Ready still describes technical file preparation. New files and copies start without a check.

After testing a scenario, open its hamburger menu → Record result…. For Windows scripts, optionally select the tested Windows image (or leave “Not selected (general check)”), choose Installation succeeded or Installation failed, optionally enter the machine/model and a comment, then save. The server records the author and date. The latest entry per image determines the current status for that image; History retains previous entries and loads longer histories in batches. Changed content requires a new check. The observation applies to this answer file and the described test conditions.

Operators and administrators record results; observers can read history. Recording does not start an installation or require menu publication. Read failures are shown as unavailable information, not an untested file. Failed submissions keep the entered fields in the dialog.
