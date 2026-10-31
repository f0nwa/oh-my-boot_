# Answer file editor

> Current installation commands use the target OMB names. Dated deployment accounts, original backup paths and outcomes describe the pre-migration stand; its migration is pending. See the [technical migration plan](../../docs/superpowers/plans/2026-10-31-omb-technical-migration.md).

“Library → Answer files → Create in editor” builds an `autounattend.xml` for Windows Setup. Every ready file in the list has an “Open in editor” button. The editor is a Go port of [cschneegans/unattend-generator](https://github.com/cschneegans/unattend-generator), the library behind [schneegans.de](https://schneegans.de/windows/unattend-generator/), so the settings and the output are those of the site.

## The page

- **Contents on the left:** 12 sections — language and region, disk and setup, edition and key, name and time, accounts, Explorer and Start, appearance, network and VMs, remove apps, tweaks and security, scripts, custom XML. Marks: “changed” — the section differs from Windows defaults; a number — how many apps, scripts or custom XML elements; “N notes” — the section has notes.
- **One section in the middle.** Above it are presets: “Windows defaults”, “Language and key only — disk by hand”, “Lab: local account, automatic sign-in”. A preset replaces all settings.
- **Tabs on the right:** “Result” — a plain summary; “XML” — the whole file with changed lines highlighted; “Notes” — for example, plain-text passwords or Windows protection turned off. “Wipes the disk” appears when the file partitions the disk itself; its menu item gets “WIPES DISK”.
- **Save to image** — ISO image, menu item title in Latin letters and folder. The file is saved as a new answer file of that image; it reaches the menu when picked in a new installation and the menu is published. Editing an existing file **always saves a new file**: the original and its menu item stay unchanged.
- **Download autounattend.xml** — the same file, for USB, ISO or checking.
- The computer name field suggests names from the machine list.
- Generator errors (“line longer than 255 characters”, “at least one administrator is needed” and so on) are shown above the form, and the input stays.
- A file over 1 MB cannot be saved to an image but can be downloaded.
- The page works without JavaScript: moving between sections, presets and “another row” submit the form. With JavaScript, sections switch in place and the summary and XML follow every change.

Permissions: everyone can open and download; administrators and operators can save to an image. Audit: `windows-answer.create` marked `editor=1`. Computers in the trusted PXE segment can fetch the file, passwords and keys included; “Obscure passwords” is only base64 encoding.

## Partitioning in network installs

- **“Windows Setup”** — the disk is chosen on the Setup screen; the answer file does not touch it. Works for every install.
- **“Wipe and partition the disk automatically”** — a generated `pe.cmd` replaces Setup in the windowsPE pass: it picks the disk (by number, by size and type, or asks), partitions it (GPT/MBR by firmware, with or without a recovery partition), applies the image with `dism`, makes the system partition bootable and restarts. `pe.cmd` looks for the image as `<drive>:\sources\install.wim`, so for network installs the WinPE script of every answer file maps the profile folder once more as `I:` and copies the answer file to `X:\autounattend.xml`. Files saved to an image before 24.10.2026 lack this mapping; save them again to use `pe.cmd`.
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
