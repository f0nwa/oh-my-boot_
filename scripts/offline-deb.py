#!/usr/bin/env python3
"""Build or verify a signed Ubuntu APT repository from OMB release archives."""

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


VERIFIER = Path(__file__).with_name("offline-release.py")
REPO_ROOT = Path(__file__).resolve().parent.parent
UBUNTU_HELPER = "/usr/lib/omb/omb-prepare-ubuntu-source"
DEPENDENCIES = ("ca-certificates, cifs-utils, curl, dnsmasq-base, nfs-common, "
                "openssl, postgresql, python3, samba, wimtools")
TARGET = {"os": "ubuntu", "version_id": "26.04", "architecture": "amd64"}
CHANNELS = ("stable", "dev")
REPO_MANIFEST = "repo-manifest.json"
REPO_SIGNATURE = "repo-manifest.sig"
ALLOWED_UPDATE_UNITS = frozenset(
    f"deploy/systemd/{name}.service"
    for name in ("omb-agent", "omb-boot", "omb-control", "omb-dnsmasq", "omb-images", "omb-winiso@")
)
ALLOWED_UPDATE_HELPERS = frozenset({
    "scripts/prepare-ubuntu-source.sh",
    "scripts/fetch-ipxe.sh",
})
BOOT_ASSETS = ("ipxeboot.tar.gz", "ipxe-shimx64.efi", "wimboot")


def package_unit(name, content):
    lines = []
    for line in content.splitlines(keepends=True):
        if line.startswith("ExecStart=/usr/local/bin/"):
            line = line.replace("ExecStart=/usr/local/bin/", "ExecStart=/usr/bin/", 1)
            if name == "omb-agent.service":
                line = f"{line.rstrip()} -ubuntu-source-script {UBUNTU_HELPER}\n"
        lines.append(line)
    return "".join(lines)


def verify(archive, public_key, output, *options):
    subprocess.run([sys.executable, VERIFIER, "verify", archive, "--public-key", public_key,
                    "--extract", output, *options], stdout=subprocess.DEVNULL, check=True)
    return json.loads((output / "manifest.json").read_text())


def stage_file(source, package, destination, mode=0o644):
    path = package / destination
    path.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, path)
    path.chmod(mode)


def stage_sql(package, release, manifest, kind):
    destination = package / "usr/share/omb" / kind
    manifest_path = destination / "migrations.json"
    destination.mkdir(parents=True)
    manifest_path.write_text(json.dumps(manifest["migrations"], indent=2) + "\n")
    for step in manifest["migrations"]:
        stage_file(release / step["path"], destination, step["path"])


def require_amd64_elf(binary):
    with binary.open("rb") as stream:
        header = stream.read(20)
    is_elf64_little_endian = header[:6] == b"\x7fELF\x02\x01"
    machine = int.from_bytes(header[18:20], "little")
    if not is_elf64_little_endian or machine != 62:
        raise ValueError(f"expected an amd64 ELF binary: {binary.name}")


def validate_update_payload(manifest):
    allowed = {step["path"] for step in manifest["migrations"]}
    allowed.update(name for name in manifest["files"] if name.startswith("bin/"))
    allowed.update(ALLOWED_UPDATE_UNITS)
    allowed.update(ALLOWED_UPDATE_HELPERS)
    allowed.update({"README.md", "go.mod", "go.sum", "provenance.json"})
    unsupported = manifest["files"].keys() - allowed
    if unsupported:
        raise ValueError(f"unsupported update payload: {', '.join(sorted(unsupported))}")


def overlay_source(base, update, rel_path):
    if update is not None:
        candidate = update / rel_path
        if candidate.is_file():
            return candidate
    return base / rel_path


def display_version(version):
    text = version.split(":", 1)[1] if ":" in version else version
    text = text.replace("~", "-")
    if text and not text.startswith("v"):
        text = "v" + text
    return text


def resolve_channel(version, explicit=None, manifest_channel=None):
    if explicit is not None:
        if explicit not in CHANNELS:
            raise ValueError(f"unsupported release channel: {explicit}")
        channel = explicit
    elif "~dev" in version:
        channel = "dev"
    elif manifest_channel is not None:
        if manifest_channel not in CHANNELS:
            raise ValueError(f"unsupported release channel: {manifest_channel}")
        channel = manifest_channel
    else:
        channel = "stable"
    if channel == "dev" and "~dev" not in version:
        raise ValueError("dev channel package version must include ~dev (e.g. 0.2.0~dev.1)")
    if channel == "stable" and "~dev" in version:
        raise ValueError("stable channel package version must not include ~dev")
    return channel


def stage_package_tree(package, base, base_manifest, version, license_path,
                       update=None, update_manifest=None, channel=None):
    manifest = update_manifest or base_manifest
    resolved_channel = resolve_channel(version, explicit=channel, manifest_channel=manifest.get("channel"))
    if update is not None:
        validate_update_payload(manifest)
    for binary in sorted((base / "bin").glob("omb-*")):
        source = overlay_source(base, update, f"bin/{binary.name}")
        require_amd64_elf(source)
        stage_file(source, package, f"usr/bin/{binary.name}", 0o755)
    if b"ubuntu-source-script" not in (package / "usr/bin/omb-agent").read_bytes():
        raise ValueError("omb-agent must support -ubuntu-source-script for the packaged helper")
    for unit in sorted((base / "deploy/systemd").glob("omb-*.service")):
        source = overlay_source(base, update, f"deploy/systemd/{unit.name}")
        path = package / "usr/lib/systemd/system" / unit.name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(package_unit(unit.name, source.read_text()))
    for template in sorted((base / "deploy/boot-assets/windows-profiles").iterdir()):
        stage_file(template, package, f"usr/share/omb/windows-profiles/{template.name}")
    for name in BOOT_ASSETS:
        stage_file(base / "assets" / name, package, f"usr/share/omb/assets/{name}")
    stage_file(overlay_source(base, update, "scripts/prepare-ubuntu-source.sh"),
               package, UBUNTU_HELPER.lstrip("/"), 0o755)
    stage_file(overlay_source(base, update, "scripts/fetch-ipxe.sh"),
               package, "usr/lib/omb/fetch-ipxe", 0o755)
    stage_file(base / "deploy/samba/smb.conf", package, "usr/share/omb/samba/smb.conf")
    stage_file(license_path, package, "usr/share/doc/omb/copyright")
    stage_sql(package, base, base_manifest, "bootstrap")
    if update is not None:
        stage_sql(package, update, update_manifest, "upgrade")
    (package / "usr/share/omb/release.json").write_text(json.dumps({
        "channel": resolved_channel,
        "commit": manifest["commit"],
        "display_version": display_version(version),
        "from_commit": manifest["from_commit"],
        "package": f"omb {version}",
        "version": version,
    }, indent=2) + "\n")
    return manifest, resolved_channel


def build_package(package, repository, base, base_manifest, version, license_path,
                  update=None, update_manifest=None, channel=None):
    manifest, resolved_channel = stage_package_tree(
        package, base, base_manifest, version, license_path,
        update=update, update_manifest=update_manifest, channel=channel,
    )
    control = package / "DEBIAN"
    control.mkdir()
    (control / "control").write_text(
        f"Package: omb\nVersion: {version}\nArchitecture: amd64\nSection: admin\n"
        "Priority: optional\nMaintainer: OMB maintainers <omb@example.invalid>\n"
        f"Depends: {DEPENDENCIES}\nX-OMB-Commit: {manifest['commit']}\n"
        f"X-OMB-Channel: {resolved_channel}\n"
        "Description: Oh My Boot network boot server\n"
        " Server binaries, systemd units and inputs for OMB site provisioning.\n"
    )
    with (control / "md5sums").open("w") as checksums:
        for path in sorted((package / "usr").rglob("*")):
            if path.is_file():
                checksums.write(f"{file_digest(path, 'md5')}  {path.relative_to(package)}\n")
    package.chmod(0o755)
    for path in package.rglob("*"):
        path.chmod(0o755 if path.is_dir() or path.stat().st_mode & 0o100 else 0o644)
    deb = repository / f"omb_{version.replace(':', '%3a')}_amd64.deb"
    if deb.exists():
        raise ValueError(f"package filename already exists in the repository: {deb.name}")
    subprocess.run(["dpkg-deb", "--root-owner-group", "-Zgzip", "--build", package, deb],
                   env={"SOURCE_DATE_EPOCH": "0", **os.environ}, check=True)
    deb.chmod(0o644)
    return {
        "package": "omb",
        "version": version,
        "display_version": display_version(version),
        "channel": resolved_channel,
        "architecture": "amd64",
        "commit": manifest["commit"],
        "from_commit": manifest["from_commit"],
        "filename": deb.name,
        "size": deb.stat().st_size,
        "sha256": file_digest(deb, "sha256"),
    }, deb


def stage_existing_base_deb(base_deb, repository, base_manifest, version, explicit_channel=None):
    base_deb = Path(base_deb)
    if not base_deb.is_file():
        raise ValueError(f"base package does not exist: {base_deb}")

    def deb_field(name):
        return subprocess.check_output(["dpkg-deb", "--field", base_deb, name], text=True).strip()

    if (deb_field("Package") != "omb" or deb_field("Architecture") != "amd64"
            or deb_field("Version") != version or deb_field("X-OMB-Commit") != base_manifest["commit"]):
        raise ValueError("base package does not match the base release archive or version")
    existing_channel = deb_field("X-OMB-Channel")
    if existing_channel and existing_channel not in CHANNELS:
        raise ValueError(f"unsupported channel in base package: {existing_channel}")
    if explicit_channel is not None and existing_channel and existing_channel != explicit_channel:
        raise ValueError(f"base package channel {existing_channel} differs from --channel {explicit_channel}")
    resolved_channel = existing_channel or resolve_channel(
        version, explicit=explicit_channel, manifest_channel=base_manifest.get("channel")
    )
    deb = repository / f"omb_{version.replace(':', '%3a')}_amd64.deb"
    if deb.exists():
        raise ValueError(f"package filename already exists in the repository: {deb.name}")
    shutil.copyfile(base_deb, deb)
    deb.chmod(0o644)
    return {
        "package": "omb",
        "version": version,
        "display_version": display_version(version),
        "channel": resolved_channel,
        "architecture": "amd64",
        "commit": base_manifest["commit"],
        "from_commit": base_manifest["from_commit"],
        "filename": deb.name,
        "size": deb.stat().st_size,
        "sha256": file_digest(deb, "sha256"),
    }, deb


def file_digest(path, algorithm):
    digest = hashlib.new(algorithm)
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def package_record(deb):
    fields = subprocess.check_output(["dpkg-deb", "--field", deb], text=True).rstrip()
    return (f"{fields}\nFilename: ./{deb.name}\nSize: {deb.stat().st_size}\n"
            f"SHA256: {file_digest(deb, 'sha256')}\n")


def parse_packages_index(index_bytes):
    text = gzip.decompress(index_bytes).decode().strip()
    entries = []
    if not text:
        return entries
    for paragraph in text.split("\n\n"):
        fields = dict(line.split(": ", 1) for line in paragraph.splitlines()
                      if ": " in line and not line.startswith(" "))
        filename = fields["Filename"]
        if filename.startswith("./"):
            filename = filename[2:]
        if "/" in filename or not filename.endswith(".deb"):
            raise ValueError(f"invalid package filename in APT index: {filename}")
        entries.append({
            "package": fields["Package"],
            "version": fields["Version"],
            "architecture": fields["Architecture"],
            "filename": filename,
            "size": int(fields["Size"]),
            "sha256": fields["SHA256"],
        })
    entries.sort(key=lambda item: (item["package"], item["version"], item["filename"]))
    return entries


def parse_go_mod(path):
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


def parse_unattend_upstream(path):
    text = Path(path).read_text(encoding="utf-8")
    match = re.search(r"\b([0-9a-f]{40})\b", text)
    if not match:
        raise ValueError(f"could not find upstream commit in {path}")
    return {"repo": "cschneegans/unattend-generator", "commit": match.group(1)}


def openssl_signature(command, data, key, sig=None):
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


def check_repo_file(repo_dir, filename, expected_size, expected_sha256):
    if "/" in filename or "\\" in filename or filename in {"", ".", ".."}:
        raise ValueError(f"invalid repository filename: {filename}")
    path = repo_dir / filename
    if not path.is_file() or path.is_symlink():
        raise ValueError(f"missing repository file: {filename}")
    if path.stat().st_size != expected_size or file_digest(path, "sha256") != expected_sha256:
        raise ValueError(f"checksum or size mismatch in repository: {filename}")


def verify_repository(repo_dir, public_key, version=None):
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
    if data.get("format") != 1 or data.get("target") != TARGET:
        raise ValueError("unsupported repository manifest format or target")
    apt_index = data["apt_index"]
    check_repo_file(repo_dir, apt_index["filename"], apt_index["size"], apt_index["sha256"])
    packages = data.get("packages")
    if not isinstance(packages, list) or not packages:
        raise ValueError("repository manifest has no OMB packages")
    expected_debs = set()
    for entry in packages:
        if entry.get("package") != "omb" or entry.get("architecture") != "amd64":
            raise ValueError("invalid OMB package entry in repository manifest")
        if "channel" in entry and entry["channel"] not in CHANNELS:
            raise ValueError("invalid package channel in repository manifest")
        check_repo_file(repo_dir, entry["filename"], entry["size"], entry["sha256"])
        expected_debs.add(entry["filename"])
    system_debs = data.get("dependencies", {}).get("system_debs", [])
    for entry in system_debs:
        check_repo_file(repo_dir, entry["filename"], entry["size"], entry["sha256"])
        expected_debs.add(entry["filename"])
    actual_debs = {p.name for p in repo_dir.iterdir() if p.name.endswith(".deb")}
    if actual_debs != expected_debs:
        extra = sorted(actual_debs - expected_debs)
        raise ValueError(f"unexpected .deb files in repository: {', '.join(extra)}")
    if version is not None:
        for entry in packages:
            if entry["version"] == version:
                return entry
        raise ValueError(f"package version not found in repository: {version}")
    return data


def build_repository(args):
    if bool(args.update_archive) != bool(args.update_version):
        raise ValueError("provide an update archive and version together")
    subprocess.run(["dpkg", "--validate-version", args.version], check=True)
    if args.update_version:
        subprocess.run(["dpkg", "--validate-version", args.update_version], check=True)
        if subprocess.run(["dpkg", "--compare-versions", args.update_version,
                           "gt", args.version]).returncode != 0:
            raise ValueError("the update version must follow the base version")
    if args.repository.exists() or args.repository.is_symlink():
        raise ValueError("use a new repository directory")
    with tempfile.TemporaryDirectory(prefix=".omb-deb-", dir=args.repository.parent) as scratch:
        root = Path(scratch)
        base = root / "base"
        base_manifest = verify(args.base_archive, args.public_key, base)
        if base_manifest["from_commit"] is not None:
            raise ValueError("the base archive must be a full installation")
        repository = root / "repository"
        shutil.copytree(base / "debs", repository)
        base_index_bytes = (repository / "Packages.gz").read_bytes()
        system_debs = parse_packages_index(base_index_bytes)
        index = gzip.decompress(base_index_bytes).decode().strip()
        if "Package: omb" in index.splitlines():
            raise ValueError("the dependency repository already contains an OMB package")
        if args.base_deb:
            pkg_a_meta, pkg_a_deb = stage_existing_base_deb(
                args.base_deb, repository, base_manifest, args.version, explicit_channel=args.channel
            )
        else:
            pkg_a_meta, pkg_a_deb = build_package(
                root / "package-a", repository, base, base_manifest,
                args.version, args.license, channel=args.channel,
            )
        package_metas = [pkg_a_meta]
        package_debs = [pkg_a_deb]
        if args.update_archive:
            update = root / "update"
            update_manifest = verify(args.update_archive, args.public_key, update,
                                     "--expect-from", base_manifest["commit"])
            pkg_b_meta, pkg_b_deb = build_package(
                root / "package-b", repository, base, base_manifest,
                args.update_version, args.license, update, update_manifest,
                channel=args.update_channel,
            )
            package_metas.append(pkg_b_meta)
            package_debs.append(pkg_b_deb)
        index += "\n\n" + "\n".join(map(package_record, package_debs))
        index_path = repository / "Packages.gz"
        index_path.write_bytes(gzip.compress(index.encode(), mtime=0))

        boot_assets = {
            name: {
                "size": (base / "assets" / name).stat().st_size,
                "sha256": file_digest(base / "assets" / name, "sha256"),
            }
            for name in BOOT_ASSETS
        }
        dependencies = {
            "system_debs": system_debs,
            "boot_assets": boot_assets,
        }
        if args.go_mod and args.go_mod.is_file():
            dependencies["go"] = parse_go_mod(args.go_mod)
        if args.unattend_upstream and args.unattend_upstream.is_file():
            dependencies["unattend_upstream"] = parse_unattend_upstream(args.unattend_upstream)

        repo_manifest = {
            "format": 1,
            "target": TARGET,
            "apt_index": {
                "filename": "Packages.gz",
                "size": index_path.stat().st_size,
                "sha256": file_digest(index_path, "sha256"),
            },
            "packages": package_metas,
            "dependencies": dependencies,
        }
        manifest_bytes = (json.dumps(repo_manifest, sort_keys=True, ensure_ascii=False, indent=2) + "\n").encode()
        (repository / REPO_MANIFEST).write_bytes(manifest_bytes)
        if args.sign_key:
            sig_bytes = openssl_signature("-sign", manifest_bytes, args.sign_key)
            (repository / REPO_SIGNATURE).write_bytes(sig_bytes)

        repository.chmod(0o755)
        for path in repository.iterdir():
            path.chmod(0o644)
        repository.rename(args.repository)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "verify-repo":
        parser = argparse.ArgumentParser(prog="offline-deb.py verify-repo",
                                         description="Verify a signed OMB APT repository.")
        parser.add_argument("repository", type=Path)
        parser.add_argument("--public-key", type=Path, required=True)
        parser.add_argument("--version", help="print FILENAME, SHA256, COMMIT for a specific OMB version")
        args = parser.parse_args(sys.argv[2:])
        try:
            result = verify_repository(args.repository, args.public_key, version=args.version)
        except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
            parser.exit(1, f"OMB repository verification failed: {error}\n")
        if args.version:
            ch = result.get("channel") or ("dev" if "~dev" in result["version"] else "stable")
            print(result["filename"], result["sha256"], result["commit"], ch, sep="\t")
        else:
            print(json.dumps({
                "target": result["target"],
                "packages": [
                    {
                        "version": p["version"],
                        "channel": p.get("channel") or ("dev" if "~dev" in p["version"] else "stable"),
                    }
                    for p in result["packages"]
                ],
                "system_debs": len(result["dependencies"]["system_debs"]),
            }, indent=2))
        return

    default_go_mod = REPO_ROOT / "go.mod"
    default_unattend = REPO_ROOT / "internal/unattend/UPSTREAM.md"
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("base_archive", type=Path)
    parser.add_argument("repository", type=Path)
    parser.add_argument("--public-key", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--channel", choices=CHANNELS, help="release channel for base package")
    parser.add_argument("--base-deb", type=Path, help="reuse an existing verified base .deb instead of rebuilding it")
    parser.add_argument("--update-archive", type=Path)
    parser.add_argument("--update-version")
    parser.add_argument("--update-channel", choices=CHANNELS, help="release channel for update package")
    parser.add_argument("--license", type=Path, required=True)
    parser.add_argument("--sign-key", type=Path, help="OpenSSL private key to sign repo-manifest.json")
    parser.add_argument("--go-mod", type=Path, default=default_go_mod if default_go_mod.is_file() else None)
    parser.add_argument("--unattend-upstream", type=Path,
                        default=default_unattend if default_unattend.is_file() else None)
    args = parser.parse_args()
    try:
        build_repository(args)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"OMB package build failed: {error}\n")


if __name__ == "__main__":
    main()
