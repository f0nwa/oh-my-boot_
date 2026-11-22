#!/usr/bin/env python3
"""Assemble or verify an OMB release using prepared binaries and offline inputs."""

import argparse
import contextlib
import gzip
import hashlib
import io
import json
import platform
import re
import shutil
import subprocess
import tarfile
import tempfile
from pathlib import Path, PurePosixPath


TARGET = {"os": "ubuntu", "version_id": "26.04", "architecture": "amd64"}
STATE = Path("/var/lib/omb-deployment/COMMIT")
MANIFEST = "manifest.json"
SIGNATURE = "manifest.sig"
BINARIES = (
    "omb-agent", "omb-agentctl", "omb-boot", "omb-control", "omb-controlctl",
    "omb-images", "omb-imagesctl", "omb-winiso-prepare",
)
UNITS = tuple(
    "deploy/systemd/" + name + ".service"
    for name in ("omb-agent", "omb-boot", "omb-control", "omb-dnsmasq", "omb-images", "omb-winiso@")
)
HELPERS = ("scripts/prepare-ubuntu-source.sh", "scripts/fetch-ipxe.sh")
BOOTSTRAP = [
    {"database": "omb_boot", "path": "deploy/sql/boot.sql"},
    {"database": "omb_boot", "path": "deploy/sql/2026-10-23-boot-transfer.sql"},
    {"database": "omb_images", "path": "deploy/sql/images.sql"},
    {"database": "omb_control", "path": "deploy/sql/control.sql"},
]
REQUIRED_INSTALL = {
    *("bin/" + name for name in BINARIES),
    "assets/ipxeboot.tar.gz", "assets/ipxe-shimx64.efi", "assets/wimboot",
    "debs/Packages.gz", "deploy/samba/smb.conf", *HELPERS,
    *(step["path"] for step in BOOTSTRAP),
    *UNITS,
    "deploy/boot-assets/windows-profiles/omb-setup.cmd.in",
    "deploy/boot-assets/windows-profiles/winpeshl.ini",
}


def portable_path(name):
    path = PurePosixPath(name)
    if (not path.parts or path.is_absolute() or ".." in path.parts or path.as_posix() != name
            or "\\" in name or any(ord(char) < 32 for char in name)):
        raise ValueError(f"invalid archive member name: {name!r}")
    return path


def commit_id(value):
    if not isinstance(value, str) or not re.fullmatch(r"[0-9a-f]{40}", value):
        raise ValueError("expected a full lowercase Git commit")
    return value


def hash_file(stream, output=None):
    digest = hashlib.sha256()
    count = 0
    for block in iter(lambda: stream.read(1024 * 1024), b""):
        digest.update(block)
        count += len(block)
        if output is not None:
            output.write(block)
    return {"size": count, "sha256": digest.hexdigest()}


def validate_manifest(data):
    if data["format"] != 1 or data["target"] != TARGET:
        raise ValueError("unsupported release format or target")
    commit = commit_id(data["commit"])
    base = data["from_commit"]
    files = data["files"]
    if not isinstance(files, dict) or not files:
        raise ValueError("release has no file inventory")
    for name, entry in files.items():
        path = portable_path(name)
        if name in {MANIFEST, SIGNATURE} or any(parent.as_posix() in files for parent in path.parents):
            raise ValueError(f"invalid payload member: {name}")
        if (type(entry["size"]) is not int or entry["size"] < 0
                or not re.fullmatch(r"[0-9a-f]{64}", entry["sha256"])):
            raise ValueError(f"invalid inventory entry: {name}")
    binaries = {path for path in files if path.startswith("bin/")}
    if binaries - {"bin/" + name for name in BINARIES}:
        raise ValueError("unrecognized binary in payload")
    steps = data["migrations"]
    updatable = binaries | (files.keys() & (set(UNITS) | set(HELPERS)))
    if base is None:
        if (steps != BOOTSTRAP or REQUIRED_INSTALL - files.keys()
                or not any(n.startswith("debs/") and n.endswith(".deb") for n in files)):
            raise ValueError("incomplete installation payload")
    elif commit_id(base) == commit or (not steps and not updatable):
        raise ValueError("update needs a distinct source commit and at least one binary, unit, helper, or migration")
    seen = set()
    for step in steps:
        name = step["path"]
        if (step["database"] not in {"omb_boot", "omb_control", "omb_images"}
                or name not in files or not name.startswith("deploy/sql/")
                or not name.endswith(".sql") or name in seen):
            raise ValueError(f"invalid migration: {name}")
        seen.add(name)


def package_index(index, files):
    """Check the local APT index covers exactly the packaged .debs."""
    expected = {n for n in files if n.startswith("debs/") and n.endswith(".deb")}
    seen = set()
    for paragraph in gzip.decompress(index).decode().strip().split("\n\n"):
        fields = dict(line.split(": ", 1) for line in paragraph.splitlines() if ": " in line and not line.startswith(" "))
        name = fields["Filename"]
        if not name.startswith("./") or "/" in name[2:]:
            raise ValueError(f"invalid package path: {name}")
        path = "debs/" + name[2:]
        if path not in expected or path in seen or fields["Architecture"] not in {"amd64", "all"}:
            raise ValueError(f"unexpected package in APT index: {path}")
        if files[path] != {"size": int(fields["Size"]), "sha256": fields["SHA256"]}:
            raise ValueError(f"package checksum or size differs from APT index: {path}")
        if not fields["Package"] or not fields["Version"]:
            raise ValueError(f"missing package name or version: {path}")
        seen.add(path)
    if seen != expected:
        raise ValueError("APT index does not cover all .deb files")


def signature(command, data, key, sig=None):
    with tempfile.TemporaryDirectory(prefix="omb-signature-") as temp:
        root = Path(temp)
        (root / MANIFEST).write_bytes(data)
        path = root / SIGNATURE
        args = ["openssl", "dgst", "-sha256", command, str(key)]
        if sig is None:
            args += ["-out", str(path)]
        else:
            path.write_bytes(sig)
            args += ["-signature", str(path)]
        result = subprocess.run([*args, str(root / MANIFEST)], capture_output=True)
        if result.returncode:
            raise ValueError("manifest signature operation failed")
        if sig is None:
            return path.read_bytes()


def add_file(archive, name, size, stream):
    info = tarfile.TarInfo(name)
    info.mode = 0o644
    info.size = size
    archive.addfile(info, stream)


def pack(args):
    root = args.payload.resolve(strict=True)
    output = args.archive.resolve()
    if root == output or root in output.parents:
        raise ValueError("archive must be outside the payload")
    paths = {}
    for path in sorted(root.rglob("*")):
        if path.is_symlink():
            raise ValueError(f"symlink in payload: {path}")
        if path.is_dir():
            continue
        if not path.is_file():
            raise ValueError(f"non-regular payload: {path}")
        paths[path.relative_to(root).as_posix()] = path
    files = {}
    for name, path in paths.items():
        with path.open("rb") as stream:
            files[name] = hash_file(stream)
    steps = [dict(zip(("database", "path"), value.split("=", 1))) for value in args.migration]
    data = {
        "format": 1, "target": TARGET, "commit": args.commit,
        "from_commit": args.from_commit, "files": files,
        "migrations": steps if args.from_commit is not None or steps else BOOTSTRAP,
    }
    validate_manifest(data)
    if "debs/Packages.gz" in files:
        package_index(paths["debs/Packages.gz"].read_bytes(), files)
    raw = (json.dumps(data, sort_keys=True, ensure_ascii=False, indent=2) + "\n").encode()
    auth = signature("-sign", raw, args.sign_key)
    with output.open("xb") as stream:
        try:
            with gzip.GzipFile(filename="", fileobj=stream, mode="wb", mtime=0) as zipped:
                with tarfile.open(fileobj=zipped, mode="w|") as archive:
                    for name, body in ((MANIFEST, raw), (SIGNATURE, auth)):
                        add_file(archive, name, len(body), io.BytesIO(body))
                    for name, path in paths.items():
                        with path.open("rb") as source:
                            add_file(archive, name, files[name]["size"], source)
        except BaseException:
            output.unlink()
            raise
    return data


def read_header(archive, name, limit):
    info = archive.next()
    if info is None or info.name != name or info.type != tarfile.REGTYPE or info.size > limit:
        raise ValueError(f"expected regular archive member {name}")
    return archive.extractfile(info).read()


def check_host(data):
    os_release = dict(line.split("=", 1) for line in Path("/etc/os-release").read_text().splitlines()
                      if line and not line.startswith("#") and "=" in line)
    actual = (os_release.get("ID", "").strip('"'), os_release.get("VERSION_ID", "").strip('"'),
              platform.machine())
    if actual != ("ubuntu", "26.04", "x86_64"):
        raise ValueError(f"unsupported target host: {actual}")
    try:
        installed = commit_id(STATE.read_text().strip())
    except FileNotFoundError:
        installed = None
    if data["from_commit"] != installed:
        raise ValueError(f"installed source commit does not match: {installed}")
    if installed is None and Path("/usr/local/bin/omb-control").exists():
        raise ValueError("OMB binary exists without a recorded release")


def verify(args):
    dest = args.extract.absolute() if args.extract else None
    if dest and (dest.exists() or dest.is_symlink()):
        raise ValueError("extraction destination already exists")
    with tarfile.open(args.archive, "r|gz") as archive, contextlib.ExitStack() as stack:
        raw = read_header(archive, MANIFEST, 4 * 1024 * 1024)
        auth = read_header(archive, SIGNATURE, 8192)
        signature("-verify", raw, args.public_key, auth)
        data = json.loads(raw)
        validate_manifest(data)
        if args.expect_from is not None and data["from_commit"] != commit_id(args.expect_from):
            raise ValueError("source commit does not match expected commit")
        if args.check_host:
            check_host(data)
        todo = dict(data["files"])
        root = None
        if dest:
            if shutil.disk_usage(dest.parent).free < sum(entry["size"] for entry in todo.values()):
                raise ValueError("insufficient free space to extract payload")
            tmp = stack.enter_context(tempfile.TemporaryDirectory(prefix=".omb-verified-", dir=dest.parent))
            root = Path(tmp) / "release"
            root.mkdir(mode=0o700)
        for member in iter(archive.next, None):
            path = portable_path(member.name)
            expected = todo.pop(member.name, None)
            if member.type != tarfile.REGTYPE or expected is None:
                raise ValueError(f"unexpected or repeated archive member: {member.name}")
            if member.size != expected["size"]:
                raise ValueError(f"size/checksum mismatch: {member.name}")
            with archive.extractfile(member) as stream:
                if root:
                    target = root / path
                    target.parent.mkdir(parents=True, exist_ok=True)
                    with target.open("xb") as output:
                        actual = hash_file(stream, output)
                else:
                    actual = hash_file(stream)
            if actual != expected:
                raise ValueError(f"checksum mismatch: {member.name}")
        if todo:
            raise ValueError(f"missing archive member: {next(iter(todo))}")
        if root:
            (root / MANIFEST).write_bytes(raw)
            (root / SIGNATURE).write_bytes(auth)
            dest.mkdir(mode=0o700)  # Reserve a previously unused name before moving.
            root.rename(dest)
    return data


def main():
    cli = argparse.ArgumentParser(description=__doc__)
    sub = cli.add_subparsers(dest="command", required=True)
    create = sub.add_parser("pack", help="pack and sign an existing payload directory")
    create.add_argument("payload", type=Path)
    create.add_argument("archive", type=Path)
    create.add_argument("--commit", required=True)
    create.add_argument("--from-commit", help="required installed commit for an update")
    create.add_argument("--migration", action="append", default=[], metavar="DATABASE=PATH")
    create.add_argument("--sign-key", required=True, type=Path)
    check = sub.add_parser("verify", help="verify a signed release before using its files")
    check.add_argument("archive", type=Path)
    check.add_argument("--public-key", required=True, type=Path)
    check.add_argument("--expect-from", help="check that the archive targets a specific source commit")
    check.add_argument("--check-host", action="store_true", help="check Ubuntu and the installed release")
    check.add_argument("--extract", type=Path, help="prepare a new directory with verified files")
    args = cli.parse_args()
    try:
        data = pack(args) if args.command == "pack" else verify(args)
    except (OSError, ValueError, EOFError, KeyError, TypeError, tarfile.TarError) as exc:
        cli.exit(1, f"error: {exc}\n")
    print(json.dumps({key: data[key] for key in ("commit", "from_commit", "target", "migrations")},
                     ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
