#!/usr/bin/env python3
"""Build or verify a signed self-contained Ubuntu APT repository for OMB."""

from __future__ import annotations

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


REPO_ROOT = Path(__file__).resolve().parent.parent
UBUNTU_HELPER = "/usr/lib/omb/omb-prepare-ubuntu-source"
DEPENDENCIES = (
    "ca-certificates, cifs-utils, curl, dnsmasq-base, nfs-common, "
    "openssl, postgresql, python3, samba, wimtools"
)
TARGET = {"os": "ubuntu", "version_id": "26.04", "architecture": "amd64"}
CHANNELS = ("stable", "dev")
REPO_MANIFEST = "repo-manifest.json"
REPO_SIGNATURE = "repo-manifest.sig"

BINARIES = (
    "omb-agent",
    "omb-agentctl",
    "omb-boot",
    "omb-control",
    "omb-controlctl",
    "omb-images",
    "omb-imagesctl",
    "omb-winiso-prepare",
)
UNITS = (
    "omb-agent.service",
    "omb-boot.service",
    "omb-control.service",
    "omb-dnsmasq.service",
    "omb-images.service",
    "omb-winiso@.service",
)
HELPERS = (
    ("scripts/prepare-ubuntu-source.sh", UBUNTU_HELPER.lstrip("/")),
    ("scripts/fetch-ipxe.sh", "usr/lib/omb/fetch-ipxe"),
)
BOOT_ASSETS = ("ipxeboot.tar.gz", "ipxe-shimx64.efi", "wimboot")
WINDOWS_PROFILES = ("omb-setup.cmd.in", "winpeshl.ini")
BOOTSTRAP_SQL = ("boot.sql", "images.sql", "control.sql")
DB_ORDER = ("omb_boot", "omb_images", "omb_control")

MIGRATION_FILENAME_RE = re.compile(r"^[0-9]{4}_[a-z0-9_]+\.sql$")
SQL_STRIP_RE = re.compile(
    r"--[^\n]*"
    r"|/\*.*?\*/"
    r"|'(?:''|[^'])*'",
    re.DOTALL,
)
FORBIDDEN_SQL_RE = re.compile(r"\b(BEGIN|COMMIT|ROLLBACK|VACUUM)\b", re.IGNORECASE)


def file_digest(path: Path, algorithm: str) -> str:
    digest = hashlib.new(algorithm)
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def commit_id(value: str) -> str:
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
        raise ValueError("expected a full lowercase 40-hex Git commit")
    return value


def display_version(version: str) -> str:
    text = version.split(":", 1)[1] if ":" in version else version
    text = text.replace("~", "-")
    if text and not text.startswith("v"):
        text = "v" + text
    return text


def resolve_channel(version: str, channel: str) -> str:
    if channel not in CHANNELS:
        raise ValueError(f"unsupported release channel: {channel}")
    if channel == "dev" and "~dev" not in version:
        raise ValueError("dev channel package version must include ~dev (e.g. 0.2.0~dev.1)")
    if channel == "stable" and "~dev" in version:
        raise ValueError("stable channel package version must not include ~dev")
    return channel


def require_regular_file(path: Path, label: str) -> Path:
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"missing required regular file: {label}")
    return path


def require_amd64_elf(binary: Path) -> None:
    with binary.open("rb") as stream:
        header = stream.read(20)
    is_elf64_little_endian = header[:6] == b"\x7fELF\x02\x01"
    machine = int.from_bytes(header[18:20], "little")
    if not is_elf64_little_endian or machine != 62:
        raise ValueError(f"expected an amd64 ELF binary: {binary.name}")


def package_unit(name: str, content: str) -> str:
    lines = []
    for line in content.splitlines(keepends=True):
        if line.startswith("ExecStart=/usr/local/bin/"):
            line = line.replace("ExecStart=/usr/local/bin/", "ExecStart=/usr/bin/", 1)
            if name == "omb-agent.service":
                line = f"{line.rstrip()} -ubuntu-source-script {UBUNTU_HELPER}\n"
        lines.append(line)
    return "".join(lines)


def stage_file(source: Path, package: Path, destination: str, mode: int = 0o644) -> None:
    path = package / destination
    path.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, path)
    path.chmod(mode)


def validate_migration_sql(sql_text: str, name: str) -> None:
    stripped = SQL_STRIP_RE.sub(" ", sql_text)
    match = FORBIDDEN_SQL_RE.search(stripped)
    if match:
        raise ValueError(
            f"migration {name} contains forbidden statement: {match.group(1).upper()}"
        )


def collect_migrations(payload: Path) -> list[dict]:
    migrations_root = payload / "deploy/sql/migrations"
    if migrations_root.is_symlink():
        raise ValueError("migrations directory must not be a symlink")
    if not migrations_root.exists():
        return []
    if not migrations_root.is_dir():
        raise ValueError("deploy/sql/migrations must be a directory")

    allowed_root = set(DB_ORDER) | {".gitkeep"}
    for entry in sorted(migrations_root.iterdir(), key=lambda p: p.name):
        if entry.name not in allowed_root:
            raise ValueError(f"unexpected entry in deploy/sql/migrations: {entry.name}")

    result: list[dict] = []
    for db in DB_ORDER:
        db_dir = migrations_root / db
        if not db_dir.exists():
            continue
        if db_dir.is_symlink() or not db_dir.is_dir():
            raise ValueError(f"migration database path must be a directory: {db}")
        seen_numbers: set[str] = set()
        for entry in sorted(db_dir.iterdir(), key=lambda p: p.name):
            if entry.name == ".gitkeep" and entry.is_file() and not entry.is_symlink():
                continue
            if entry.is_symlink() or not entry.is_file():
                raise ValueError(f"invalid migration entry: {db}/{entry.name}")
            if not MIGRATION_FILENAME_RE.match(entry.name):
                raise ValueError(f"invalid migration filename: {db}/{entry.name}")
            number = entry.name[:4]
            if number in seen_numbers:
                raise ValueError(f"duplicate migration number {number} in {db}")
            seen_numbers.add(number)
            sql_text = entry.read_text(encoding="utf-8")
            validate_migration_sql(sql_text, f"{db}/{entry.name}")
            result.append({
                "database": db,
                "id": entry.stem,
                "path": f"{db}/{entry.name}",
                "sha256": file_digest(entry, "sha256"),
            })
    return result


def parse_packages_index(index_bytes: bytes) -> tuple[list[dict], str]:
    text = gzip.decompress(index_bytes).decode("utf-8").strip()
    entries: list[dict] = []
    if not text:
        return entries, text
    for paragraph in text.split("\n\n"):
        fields = dict(
            line.split(": ", 1)
            for line in paragraph.splitlines()
            if ": " in line and not line.startswith(" ")
        )
        filename = fields["Filename"]
        if filename.startswith("./"):
            filename = filename[2:]
        if "/" in filename or not filename.endswith(".deb"):
            raise ValueError(f"invalid package filename in APT index: {filename}")
        if fields["Architecture"] not in {"amd64", "all"}:
            raise ValueError(f"unsupported architecture in APT index: {filename}")
        if not fields.get("Package") or not fields.get("Version"):
            raise ValueError(f"missing package name or version in APT index: {filename}")
        entries.append({
            "package": fields["Package"],
            "version": fields["Version"],
            "architecture": fields["Architecture"],
            "filename": filename,
            "size": int(fields["Size"]),
            "sha256": fields["SHA256"],
        })
    entries.sort(key=lambda item: (item["package"], item["version"], item["filename"]))
    return entries, text


def validate_system_debs(debs_dir: Path) -> tuple[list[dict], str]:
    if not debs_dir.is_dir() or debs_dir.is_symlink():
        raise ValueError("missing payload directory: debs")
    index_path = require_regular_file(debs_dir / "Packages.gz", "debs/Packages.gz")
    entries, index_text = parse_packages_index(index_path.read_bytes())
    if not entries:
        raise ValueError("debs/Packages.gz contains no system packages")
    if any(entry["package"] == "omb" for entry in entries):
        raise ValueError("the dependency repository already contains an OMB package")

    actual_debs: set[str] = set()
    for path in debs_dir.iterdir():
        if path.name == "Packages.gz":
            continue
        if path.is_symlink() or not path.is_file() or not path.name.endswith(".deb"):
            raise ValueError(f"unexpected file in payload debs/: {path.name}")
        actual_debs.add(path.name)

    indexed_debs: set[str] = set()
    for entry in entries:
        filename = entry["filename"]
        if filename in indexed_debs:
            raise ValueError(f"duplicate package entry in debs/Packages.gz: {filename}")
        indexed_debs.add(filename)
        deb_path = debs_dir / filename
        if not deb_path.is_file() or deb_path.is_symlink():
            raise ValueError(f"missing system .deb referenced by Packages.gz: {filename}")
        if deb_path.stat().st_size != entry["size"] or file_digest(deb_path, "sha256") != entry["sha256"]:
            raise ValueError(f"system .deb checksum or size mismatch: {filename}")

    if actual_debs != indexed_debs:
        extra = sorted(actual_debs - indexed_debs)
        raise ValueError(f"system .deb files not covered by Packages.gz: {', '.join(extra)}")

    return entries, index_text


def package_record(deb: Path) -> str:
    fields = subprocess.check_output(["dpkg-deb", "--field", str(deb)], text=True).rstrip()
    return (
        f"{fields}\nFilename: ./{deb.name}\nSize: {deb.stat().st_size}\n"
        f"SHA256: {file_digest(deb, 'sha256')}\n"
    )


def parse_go_mod(path: Path) -> dict:
    go_version = None
    modules = []
    in_require = False
    for raw_line in Path(path).read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("//"):
            continue
        if line.startswith("go "):
            go_version = line.split()[1]
            continue
        if line == "require (":
            in_require = True
            continue
        if in_require and line == ")":
            in_require = False
            continue
        if in_require or line.startswith("require "):
            body = line[len("require "):].strip() if line.startswith("require ") else line
            indirect = "// indirect" in body
            parts = body.split("//", 1)[0].split()
            if len(parts) >= 2:
                modules.append({"path": parts[0], "version": parts[1], "indirect": indirect})
    modules.sort(key=lambda item: item["path"])
    return {"version": go_version, "modules": modules}


def parse_unattend_upstream(path: Path) -> dict:
    text = Path(path).read_text(encoding="utf-8")
    match = re.search(r"\b([0-9a-f]{40})\b", text)
    if not match:
        raise ValueError(f"could not find upstream commit in {path}")
    return {"repo": "cschneegans/unattend-generator", "commit": match.group(1)}


def openssl_signature(command: str, data: bytes, key: Path, sig: bytes | None = None) -> bytes | None:
    with tempfile.TemporaryDirectory(prefix="omb-repo-sig-") as temp:
        root = Path(temp)
        manifest_file = root / REPO_MANIFEST
        manifest_file.write_bytes(data)
        sig_file = root / REPO_SIGNATURE
        cmd = ["openssl", "dgst", "-sha256", command, str(key)]
        if sig is None:
            cmd += ["-out", str(sig_file)]
        else:
            sig_file.write_bytes(sig)
            cmd += ["-signature", str(sig_file)]
        result = subprocess.run([*cmd, str(manifest_file)], capture_output=True)
        if result.returncode != 0:
            raise ValueError("repository manifest signature operation failed")
        if sig is None:
            return sig_file.read_bytes()
    return None


def stage_package_tree(
    package: Path,
    payload: Path,
    version: str,
    channel: str,
    commit: str,
    license_path: Path,
    migrations: list[dict],
) -> None:
    bin_dir = payload / "bin"
    if not bin_dir.is_dir() or bin_dir.is_symlink():
        raise ValueError("missing payload directory: bin")
    actual_bins = {p.name for p in bin_dir.iterdir() if p.name.startswith("omb-")}
    missing_bins = sorted(set(BINARIES) - actual_bins)
    if missing_bins:
        raise ValueError(f"missing required binaries: {', '.join(missing_bins)}")
    extra_bins = sorted(actual_bins - set(BINARIES))
    if extra_bins:
        raise ValueError(f"unrecognized binaries in payload: {', '.join(extra_bins)}")
    for name in BINARIES:
        source = require_regular_file(bin_dir / name, f"bin/{name}")
        require_amd64_elf(source)
        stage_file(source, package, f"usr/bin/{name}", 0o755)
    if b"ubuntu-source-script" not in (package / "usr/bin/omb-agent").read_bytes():
        raise ValueError("omb-agent must support -ubuntu-source-script for the packaged helper")

    systemd_dir = payload / "deploy/systemd"
    if not systemd_dir.is_dir() or systemd_dir.is_symlink():
        raise ValueError("missing payload directory: deploy/systemd")
    for unit_name in UNITS:
        source = require_regular_file(systemd_dir / unit_name, f"deploy/systemd/{unit_name}")
        dest = package / "usr/lib/systemd/system" / unit_name
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(package_unit(unit_name, source.read_text(encoding="utf-8")), encoding="utf-8")

    profiles_dir = payload / "deploy/boot-assets/windows-profiles"
    if not profiles_dir.is_dir() or profiles_dir.is_symlink():
        raise ValueError("missing payload directory: deploy/boot-assets/windows-profiles")
    for required_profile in WINDOWS_PROFILES:
        require_regular_file(
            profiles_dir / required_profile,
            f"deploy/boot-assets/windows-profiles/{required_profile}",
        )
    for template in sorted(profiles_dir.iterdir(), key=lambda p: p.name):
        require_regular_file(template, f"deploy/boot-assets/windows-profiles/{template.name}")
        stage_file(template, package, f"usr/share/omb/windows-profiles/{template.name}")

    for asset_name in BOOT_ASSETS:
        source = require_regular_file(payload / "assets" / asset_name, f"assets/{asset_name}")
        stage_file(source, package, f"usr/share/omb/assets/{asset_name}")

    for src_rel, dst_rel in HELPERS:
        source = require_regular_file(payload / src_rel, src_rel)
        stage_file(source, package, dst_rel, 0o755)

    smb_conf = require_regular_file(payload / "deploy/samba/smb.conf", "deploy/samba/smb.conf")
    stage_file(smb_conf, package, "usr/share/omb/samba/smb.conf")

    stage_file(require_regular_file(license_path, str(license_path)), package, "usr/share/doc/omb/copyright")

    for sql_name in BOOTSTRAP_SQL:
        source = require_regular_file(payload / "deploy/sql" / sql_name, f"deploy/sql/{sql_name}")
        stage_file(source, package, f"usr/share/omb/bootstrap/{sql_name}")

    migrations_dest = package / "usr/share/omb/migrations"
    migrations_dest.mkdir(parents=True, exist_ok=True)
    for entry in migrations:
        src = payload / "deploy/sql/migrations" / entry["path"]
        stage_file(src, package, f"usr/share/omb/migrations/{entry['path']}")
    (migrations_dest / "manifest.json").write_text(
        json.dumps({"format": 1, "migrations": migrations}, indent=2) + "\n",
        encoding="utf-8",
    )

    (package / "usr/share/omb/release.json").write_text(
        json.dumps(
            {
                "channel": channel,
                "commit": commit,
                "display_version": display_version(version),
                "package": f"omb {version}",
                "version": version,
            },
            indent=2,
        )
        + "\n",
        encoding="utf-8",
    )


def build_package(
    package: Path,
    repository: Path,
    payload: Path,
    version: str,
    channel: str,
    commit: str,
    license_path: Path,
    migrations: list[dict],
) -> tuple[dict, Path]:
    stage_package_tree(package, payload, version, channel, commit, license_path, migrations)

    control = package / "DEBIAN"
    control.mkdir()
    (control / "control").write_text(
        f"Package: omb\nVersion: {version}\nArchitecture: amd64\nSection: admin\n"
        "Priority: optional\nMaintainer: OMB maintainers <omb@example.invalid>\n"
        f"Depends: {DEPENDENCIES}\nX-OMB-Commit: {commit}\n"
        f"X-OMB-Channel: {channel}\n"
        "Description: Oh My Boot network boot server\n"
        " Server binaries, systemd units and inputs for OMB site provisioning.\n",
        encoding="utf-8",
    )
    with (control / "md5sums").open("w", encoding="utf-8") as checksums:
        for path in sorted((package / "usr").rglob("*")):
            if path.is_file():
                checksums.write(f"{file_digest(path, 'md5')}  {path.relative_to(package)}\n")

    package.chmod(0o755)
    for path in package.rglob("*"):
        path.chmod(0o755 if path.is_dir() or path.stat().st_mode & 0o100 else 0o644)

    deb = repository / f"omb_{version.replace(':', '%3a')}_amd64.deb"
    if deb.exists():
        raise ValueError(f"package filename already exists in the repository: {deb.name}")
    subprocess.run(
        ["dpkg-deb", "--root-owner-group", "-Zgzip", "--build", str(package), str(deb)],
        env={"SOURCE_DATE_EPOCH": "0", **os.environ},
        check=True,
    )
    deb.chmod(0o644)
    return {
        "package": "omb",
        "version": version,
        "display_version": display_version(version),
        "channel": channel,
        "architecture": "amd64",
        "commit": commit,
        "filename": deb.name,
        "size": deb.stat().st_size,
        "sha256": file_digest(deb, "sha256"),
    }, deb


def build_repo(args: argparse.Namespace) -> None:
    commit = commit_id(args.commit)
    channel = resolve_channel(args.version, args.channel)
    subprocess.run(["dpkg", "--validate-version", args.version], check=True)
    require_regular_file(args.license, str(args.license))
    require_regular_file(args.sign_key, str(args.sign_key))

    payload = args.payload
    if not payload.is_dir() or payload.is_symlink():
        raise ValueError(f"payload directory does not exist: {payload}")
    if args.repository.exists() or args.repository.is_symlink():
        raise ValueError("use a new repository directory")

    migrations = collect_migrations(payload)
    system_debs, base_index_text = validate_system_debs(payload / "debs")

    args.repository.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".omb-deb-", dir=args.repository.parent) as scratch:
        root = Path(scratch)
        repository = root / "repository"
        repository.mkdir(mode=0o755)

        for entry in system_debs:
            src = payload / "debs" / entry["filename"]
            dst = repository / entry["filename"]
            shutil.copyfile(src, dst)
            dst.chmod(0o644)

        pkg_meta, pkg_deb = build_package(
            root / "package",
            repository,
            payload,
            args.version,
            channel,
            commit,
            args.license,
            migrations,
        )

        combined_index = base_index_text + "\n\n" + package_record(pkg_deb)
        index_path = repository / "Packages.gz"
        index_path.write_bytes(gzip.compress(combined_index.encode("utf-8"), mtime=0))

        boot_assets = {
            name: {
                "size": (payload / "assets" / name).stat().st_size,
                "sha256": file_digest(payload / "assets" / name, "sha256"),
            }
            for name in BOOT_ASSETS
        }
        dependencies: dict = {
            "boot_assets": boot_assets,
        }
        if args.go_mod and args.go_mod.is_file():
            dependencies["go"] = parse_go_mod(args.go_mod)
        if args.unattend_upstream and args.unattend_upstream.is_file():
            dependencies["unattend_upstream"] = parse_unattend_upstream(args.unattend_upstream)

        repo_manifest = {
            "format": 2,
            "target": TARGET,
            "package": pkg_meta,
            "packages_index": {
                "filename": "Packages.gz",
                "size": index_path.stat().st_size,
                "sha256": file_digest(index_path, "sha256"),
            },
            "system_debs": system_debs,
            "dependencies": dependencies,
        }
        manifest_bytes = (
            json.dumps(repo_manifest, sort_keys=True, ensure_ascii=False, indent=2) + "\n"
        ).encode("utf-8")
        (repository / REPO_MANIFEST).write_bytes(manifest_bytes)
        sig_bytes = openssl_signature("-sign", manifest_bytes, args.sign_key)
        assert sig_bytes is not None
        (repository / REPO_SIGNATURE).write_bytes(sig_bytes)

        repository.chmod(0o755)
        for path in repository.iterdir():
            path.chmod(0o644)
        repository.rename(args.repository)


def check_repo_file(repo_dir: Path, filename: str, expected_size: int, expected_sha256: str) -> None:
    if "/" in filename or "\\" in filename or filename in {"", ".", ".."}:
        raise ValueError(f"invalid repository filename: {filename}")
    path = repo_dir / filename
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"missing repository file: {filename}")
    if path.stat().st_size != expected_size or file_digest(path, "sha256") != expected_sha256:
        raise ValueError(f"checksum or size mismatch in repository: {filename}")


def verify_repo(repo_dir: Path, public_key: Path) -> dict:
    repo_dir = Path(repo_dir)
    manifest_path = repo_dir / REPO_MANIFEST
    sig_path = repo_dir / REPO_SIGNATURE
    if not manifest_path.is_file() or manifest_path.is_symlink():
        raise ValueError(f"missing {REPO_MANIFEST}")
    if not sig_path.is_file() or sig_path.is_symlink():
        raise ValueError(f"missing {REPO_SIGNATURE}")

    raw = manifest_path.read_bytes()
    openssl_signature("-verify", raw, public_key, sig_path.read_bytes())
    data = json.loads(raw)

    if data.get("format") != 2 or data.get("target") != TARGET:
        raise ValueError("unsupported repository manifest format or target")

    packages_index = data["packages_index"]
    if packages_index.get("filename") != "Packages.gz":
        raise ValueError("invalid packages_index filename in repository manifest")
    check_repo_file(repo_dir, packages_index["filename"], packages_index["size"], packages_index["sha256"])

    pkg = data.get("package")
    if not isinstance(pkg, dict):
        raise ValueError("repository manifest has no OMB package")
    if pkg.get("package") != "omb" or pkg.get("architecture") != "amd64":
        raise ValueError("invalid OMB package entry in repository manifest")
    resolve_channel(pkg["version"], pkg["channel"])
    commit_id(pkg["commit"])
    check_repo_file(repo_dir, pkg["filename"], pkg["size"], pkg["sha256"])

    system_debs = data.get("system_debs")
    if not isinstance(system_debs, list) or not system_debs:
        raise ValueError("repository manifest has no system_debs")
    expected_debs = {pkg["filename"]}
    for entry in system_debs:
        check_repo_file(repo_dir, entry["filename"], entry["size"], entry["sha256"])
        expected_debs.add(entry["filename"])

    actual_debs = {p.name for p in repo_dir.iterdir() if p.name.endswith(".deb")}
    if actual_debs != expected_debs:
        extra = sorted(actual_debs - expected_debs)
        missing = sorted(expected_debs - actual_debs)
        raise ValueError(f"unexpected .deb set in repository (extra={extra}, missing={missing})")

    return data


def main() -> None:
    default_go_mod = REPO_ROOT / "go.mod"
    default_unattend = REPO_ROOT / "internal/unattend/UPSTREAM.md"

    cli = argparse.ArgumentParser(description=__doc__)
    sub = cli.add_subparsers(dest="command", required=True)

    build_cmd = sub.add_parser("build", help="build and sign a self-contained OMB APT repository")
    build_cmd.add_argument("payload", type=Path)
    build_cmd.add_argument("repository", type=Path)
    build_cmd.add_argument("--version", required=True)
    build_cmd.add_argument("--channel", choices=CHANNELS, required=True)
    build_cmd.add_argument("--commit", required=True)
    build_cmd.add_argument("--license", type=Path, required=True)
    build_cmd.add_argument("--sign-key", type=Path, required=True)
    build_cmd.add_argument("--go-mod", type=Path, default=default_go_mod if default_go_mod.is_file() else None)
    build_cmd.add_argument(
        "--unattend-upstream",
        type=Path,
        default=default_unattend if default_unattend.is_file() else None,
    )

    verify_cmd = sub.add_parser("verify-repo", help="verify a signed OMB APT repository")
    verify_cmd.add_argument("repository", type=Path)
    verify_cmd.add_argument("--public-key", type=Path, required=True)
    verify_cmd.add_argument(
        "--print-package",
        action="store_true",
        help="print FILENAME, SHA256, VERSION, COMMIT, CHANNEL as TSV",
    )

    args = cli.parse_args()
    if args.command == "build":
        try:
            build_repo(args)
        except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
            cli.exit(1, f"OMB package build failed: {error}\n")
        return

    try:
        data = verify_repo(args.repository, args.public_key)
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        cli.exit(1, f"OMB repository verification failed: {error}\n")

    pkg = data["package"]
    if args.print_package:
        print(pkg["filename"], pkg["sha256"], pkg["version"], pkg["commit"], pkg["channel"], sep="\t")
    else:
        print(
            json.dumps(
                {
                    "target": data["target"],
                    "package": {
                        "version": pkg["version"],
                        "channel": pkg["channel"],
                        "commit": pkg["commit"],
                        "filename": pkg["filename"],
                    },
                    "system_debs": len(data["system_debs"]),
                },
                indent=2,
            )
        )


if __name__ == "__main__":
    main()
