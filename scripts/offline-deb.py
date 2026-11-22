#!/usr/bin/env python3
"""Build an Ubuntu APT repository from verified OMB release archives."""

import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile


VERIFIER = Path(__file__).with_name("offline-release.py")
UBUNTU_HELPER = "/usr/lib/omb/omb-prepare-ubuntu-source"
DEPENDENCIES = ("ca-certificates, cifs-utils, curl, dnsmasq-base, nfs-common, "
                "openssl, postgresql, python3, samba, wimtools")
ALLOWED_UPDATE_UNITS = frozenset(
    f"deploy/systemd/{name}.service"
    for name in ("omb-agent", "omb-boot", "omb-control", "omb-dnsmasq", "omb-images", "omb-winiso@")
)
ALLOWED_UPDATE_HELPERS = frozenset({
    "scripts/prepare-ubuntu-source.sh",
    "scripts/fetch-ipxe.sh",
})


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


def stage_package_tree(package, base, base_manifest, version, license_path,
                       update=None, update_manifest=None):
    manifest = update_manifest or base_manifest
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
    for name in ("ipxeboot.tar.gz", "ipxe-shimx64.efi", "wimboot"):
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
        "commit": manifest["commit"], "from_commit": manifest["from_commit"], "version": version,
    }, indent=2) + "\n")
    return manifest


def build_package(package, repository, base, base_manifest, version, license_path,
                  update=None, update_manifest=None):
    manifest = stage_package_tree(package, base, base_manifest, version, license_path,
                                  update=update, update_manifest=update_manifest)

    control = package / "DEBIAN"
    control.mkdir()
    (control / "control").write_text(
        f"Package: omb\nVersion: {version}\nArchitecture: amd64\nSection: admin\n"
        "Priority: optional\nMaintainer: OMB maintainers <omb@example.invalid>\n"
        f"Depends: {DEPENDENCIES}\nX-OMB-Commit: {manifest['commit']}\n"
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
    return deb


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
        index = gzip.decompress((repository / "Packages.gz").read_bytes()).decode().strip()
        if "Package: omb" in index.splitlines():
            raise ValueError("the dependency repository already contains an OMB package")
        packages = [build_package(root / "package-a", repository, base, base_manifest,
                                  args.version, args.license)]
        if args.update_archive:
            update = root / "update"
            update_manifest = verify(args.update_archive, args.public_key, update,
                                     "--expect-from", base_manifest["commit"])
            packages.append(build_package(root / "package-b", repository, base, base_manifest,
                                          args.update_version, args.license, update, update_manifest))
        index += "\n\n" + "\n".join(map(package_record, packages))
        (repository / "Packages.gz").write_bytes(gzip.compress(index.encode(), mtime=0))
        repository.chmod(0o755)
        for path in repository.iterdir():
            path.chmod(0o644)
        repository.rename(args.repository)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("base_archive", type=Path)
    parser.add_argument("repository", type=Path)
    parser.add_argument("--public-key", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--update-archive", type=Path)
    parser.add_argument("--update-version")
    parser.add_argument("--license", type=Path, required=True)
    args = parser.parse_args()
    try:
        build_repository(args)
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"OMB package build failed: {error}\n")


if __name__ == "__main__":
    main()
