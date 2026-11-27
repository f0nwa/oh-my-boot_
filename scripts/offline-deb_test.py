#!/usr/bin/env python3
"""Unit tests for scripts/offline-deb.py."""

from __future__ import annotations

import gzip
import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("offline-deb.py")
COMMIT_SHA = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
AMD64_ELF_PREFIX = b"\x7fELF\x02\x01" + bytes(12) + (62).to_bytes(2, "little")

BINARIES = (
    "omb",
    "omb-agent",
    "omb-agentctl",
    "omb-boot",
    "omb-control",
    "omb-controlctl",
    "omb-images",
    "omb-imagesctl",
    "omb-setup",
    "omb-update",
    "omb-winiso-prepare",
)
UNITS = (
    "omb-agent.service",
    "omb-boot.service",
    "omb-control.service",
    "omb-dnsmasq.service",
    "omb-images.service",
    "omb-update.service",
    "omb-update.socket",
    "omb-winiso@.service",
)
BOOT_ASSETS = ("ipxeboot.tar.gz", "ipxe-shimx64.efi", "wimboot")

DPKG_STUB = """#!/usr/bin/env python3
import re
import sys

if len(sys.argv) == 3 and sys.argv[1] == "--validate-version":
    ver = sys.argv[2]
    if re.fullmatch(r"(?:[0-9]+:)?[0-9][0-9A-Za-z.+:~-]*", ver):
        sys.exit(0)
    sys.stderr.write(f"invalid version: {ver}\\n")
    sys.exit(2)

sys.stderr.write(f"unsupported dpkg stub args: {sys.argv[1:]}\\n")
sys.exit(2)
"""

DPKG_DEB_STUB = """#!/usr/bin/env python3
import gzip
import io
import os
from pathlib import Path
import sys
import tarfile


def ar_member(name: str, data: bytes) -> bytes:
    header = (
        f"{name:<16}"
        f"{0:<12}"
        f"{0:<6}"
        f"{0:<6}"
        f"{'100644':<8}"
        f"{len(data):<10}"
        "`\\n"
    ).encode("ascii")
    pad = b"\\n" if len(data) % 2 == 1 else b""
    return header + data + pad


def parse_ar(raw: bytes) -> dict:
    if not raw.startswith(b"!<arch>\\n"):
        raise ValueError("not an ar archive")
    pos = 8
    members = {}
    while pos < len(raw):
        header = raw[pos:pos + 60]
        if len(header) < 60:
            break
        name = header[:16].decode("ascii").strip().rstrip("/")
        size = int(header[48:58].decode("ascii").strip())
        pos += 60
        members[name] = raw[pos:pos + size]
        pos += size + (size % 2)
    return members


def make_tar_gz(root: Path, exclude_debian: bool = False) -> bytes:
    buf = io.BytesIO()
    with gzip.GzipFile(filename="", fileobj=buf, mode="wb", mtime=0) as gz:
        with tarfile.open(fileobj=gz, mode="w") as tar:
            for path in sorted(root.rglob("*")):
                rel = path.relative_to(root)
                if exclude_debian and rel.parts[0] == "DEBIAN":
                    continue
                info = tarfile.TarInfo(name="./" + rel.as_posix())
                info.uid = 0
                info.gid = 0
                info.uname = "root"
                info.gname = "root"
                info.mtime = 0
                if path.is_dir():
                    info.type = tarfile.DIRTYPE
                    info.mode = 0o755
                    tar.addfile(info)
                elif path.is_file():
                    data = path.read_bytes()
                    info.size = len(data)
                    info.mode = path.stat().st_mode & 0o777
                    tar.addfile(info, io.BytesIO(data))
    return buf.getvalue()


args = sys.argv[1:]
if "--build" in args:
    idx = args.index("--build")
    pkg_dir = Path(args[idx + 1])
    out_deb = Path(args[idx + 2])
    control_tgz = make_tar_gz(pkg_dir / "DEBIAN", exclude_debian=False)
    data_tgz = make_tar_gz(pkg_dir, exclude_debian=True)
    blob = (
        b"!<arch>\\n"
        + ar_member("debian-binary", b"2.0\\n")
        + ar_member("control.tar.gz", control_tgz)
        + ar_member("data.tar.gz", data_tgz)
    )
    out_deb.write_bytes(blob)
    sys.exit(0)

if len(args) >= 2 and args[0] == "--field":
    deb = Path(args[1])
    members = parse_ar(deb.read_bytes())
    with tarfile.open(fileobj=io.BytesIO(members["control.tar.gz"]), mode="r:gz") as tar:
        control_text = None
        for member in tar.getmembers():
            if member.name in ("./control", "control"):
                control_text = tar.extractfile(member).read().decode("utf-8")
                break
    if control_text is None:
        sys.exit(1)
    if len(args) == 2:
        sys.stdout.write(control_text)
        sys.exit(0)
    field_name = args[2]
    for line in control_text.splitlines():
        if line.startswith(field_name + ":"):
            sys.stdout.write(line.split(":", 1)[1].strip() + "\\n")
            sys.exit(0)
    sys.exit(0)

if len(args) == 3 and args[0] == "-x":
    deb = Path(args[1])
    dest = Path(args[2])
    dest.mkdir(parents=True, exist_ok=True)
    members = parse_ar(deb.read_bytes())
    with tarfile.open(fileobj=io.BytesIO(members["data.tar.gz"]), mode="r:gz") as tar:
        tar.extractall(path=dest)
    sys.exit(0)

sys.stderr.write(f"unsupported dpkg-deb stub args: {args}\\n")
sys.exit(2)
"""


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class OfflineDebTestCase(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls._class_tmp = tempfile.TemporaryDirectory(prefix="omb-offline-deb-test-")
        cls.keys_dir = Path(cls._class_tmp.name)
        cls.private_key = cls.keys_dir / "signing-private.pem"
        cls.public_key = cls.keys_dir / "signing-public.pem"
        subprocess.run(
            ["openssl", "genpkey", "-algorithm", "RSA", "-pkeyopt", "rsa_keygen_bits:2048",
             "-out", str(cls.private_key)],
            check=True,
            capture_output=True,
        )
        subprocess.run(
            ["openssl", "pkey", "-in", str(cls.private_key), "-pubout",
             "-out", str(cls.public_key)],
            check=True,
            capture_output=True,
        )

        cls.base_env = dict(os.environ)
        if shutil.which("dpkg-deb") is None or shutil.which("dpkg") is None:
            stub_bin = cls.keys_dir / "stub-bin"
            stub_bin.mkdir()
            for name, content in (("dpkg", DPKG_STUB), ("dpkg-deb", DPKG_DEB_STUB)):
                tool = stub_bin / name
                tool.write_text(content, encoding="utf-8")
                tool.chmod(stat.S_IRWXU)
            cls.base_env["PATH"] = f"{stub_bin}{os.pathsep}{cls.base_env.get('PATH', '')}"

    @classmethod
    def tearDownClass(cls):
        cls._class_tmp.cleanup()

    def make_payload(self, root: Path, migrations: dict[str, dict[str, str]] | None = None) -> tuple[Path, Path]:
        payload = root / "payload"
        payload.mkdir()

        for binary in BINARIES:
            path = payload / "bin" / binary
            path.parent.mkdir(parents=True, exist_ok=True)
            extra = b"ubuntu-source-script" if binary == "omb-agent" else b"payload"
            path.write_bytes(AMD64_ELF_PREFIX + extra)
            path.chmod(0o755)

        for unit in UNITS:
            path = payload / "deploy/systemd" / unit
            path.parent.mkdir(parents=True, exist_ok=True)
            svc_bin = unit.removesuffix(".service")
            path.write_text(
                f"[Unit]\nDescription={unit}\n[Service]\nExecStart=/usr/local/bin/{svc_bin}\n",
                encoding="utf-8",
            )

        for rel, body in (
            ("deploy/boot-assets/windows-profiles/omb-setup.cmd.in", "@echo off\n"),
            ("deploy/boot-assets/windows-profiles/winpeshl.ini", "[LaunchApps]\n"),
            ("deploy/samba/smb.conf", "[global]\nworkgroup = WORKGROUP\n"),
            ("deploy/sql/boot.sql", "CREATE TABLE schema_migrations (id TEXT PRIMARY KEY);\n"),
            ("deploy/sql/images.sql", "CREATE TABLE schema_migrations (id TEXT PRIMARY KEY);\n"),
            ("deploy/sql/control.sql", "CREATE TABLE schema_migrations (id TEXT PRIMARY KEY);\n"),
            ("scripts/prepare-ubuntu-source.sh", "#!/bin/sh\nexit 0\n"),
            ("scripts/fetch-ipxe.sh", "#!/bin/sh\nexit 0\n"),
            ("scripts/offline-deploy.sh", "#!/bin/sh\nexit 0\n"),
            ("scripts/offline-install-lib.sh", "#!/bin/sh\nexit 0\n"),
            ("scripts/offline-deb.py", "#!/usr/bin/env python3\n"),
        ):
            path = payload / rel
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(body, encoding="utf-8")

        for asset in BOOT_ASSETS:
            path = payload / "assets" / asset
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(f"asset:{asset}\n".encode("utf-8"))

        for db in ("omb_boot", "omb_images", "omb_control"):
            db_dir = payload / "deploy/sql/migrations" / db
            db_dir.mkdir(parents=True, exist_ok=True)
            (db_dir / ".gitkeep").write_bytes(b"")

        if migrations:
            for db, files in migrations.items():
                db_dir = payload / "deploy/sql/migrations" / db
                db_dir.mkdir(parents=True, exist_ok=True)
                for filename, sql in files.items():
                    (db_dir / filename).write_text(sql, encoding="utf-8")

        debs_dir = payload / "debs"
        debs_dir.mkdir()
        sys_deb_name = "dummy-dep_1.0-1_amd64.deb"
        sys_deb_bytes = b"!<arch>\n" + b"dummy-system-deb-content"
        (debs_dir / sys_deb_name).write_bytes(sys_deb_bytes)
        packages_txt = (
            f"Package: dummy-dep\n"
            f"Version: 1.0-1\n"
            f"Architecture: amd64\n"
            f"Filename: ./{sys_deb_name}\n"
            f"Size: {len(sys_deb_bytes)}\n"
            f"SHA256: {sha256_bytes(sys_deb_bytes)}\n"
        )
        (debs_dir / "Packages.gz").write_bytes(gzip.compress(packages_txt.encode("utf-8"), mtime=0))

        license_file = root / "LICENSE"
        license_file.write_text("MIT License\n", encoding="utf-8")
        return payload, license_file

    def run_cli(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, str(SCRIPT), *args],
            env=self.base_env,
            capture_output=True,
            text=True,
        )

    def extract_deb(self, deb_path: Path, dest: Path) -> None:
        subprocess.run(
            ["dpkg-deb", "-x", str(deb_path), str(dest)],
            env=self.base_env,
            check=True,
            capture_output=True,
        )

    def test_build_and_verify_repo_empty_migrations(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            payload, license_file = self.make_payload(root)
            repo = root / "apt-repo"

            build = self.run_cli(
                "build", str(payload), str(repo),
                "--version", "0.2.0",
                "--channel", "stable",
                "--commit", COMMIT_SHA,
                "--license", str(license_file),
                "--sign-key", str(self.private_key),
            )
            self.assertEqual(build.returncode, 0, msg=build.stderr)

            archive_path = root / "omb-update.tar.gz"
            archive = self.run_cli("pack-update", str(repo), str(archive_path), "--public-key", str(self.public_key))
            self.assertEqual(archive.returncode, 0, msg=archive.stderr)
            with tarfile.open(archive_path, "r:gz") as packed:
                self.assertEqual(set(packed.getnames()), {
                    "apt-repo/" + filename.name for filename in repo.iterdir()
                })
                for item in packed:
                    self.assertTrue(item.isfile())
                    self.assertEqual(packed.extractfile(item).read(), (root / item.name).read_bytes())
            (repo / "Packages.gz").write_bytes(b"tampered")
            rejected = self.run_cli("pack-update", str(repo), str(root / "bad.tar.gz"), "--public-key", str(self.public_key))
            self.assertNotEqual(rejected.returncode, 0)
            self.assertFalse((root / "bad.tar.gz").exists())
            with tarfile.open(archive_path, "r:gz") as packed:
                (repo / "Packages.gz").write_bytes(packed.extractfile("apt-repo/Packages.gz").read())

            verify = self.run_cli(
                "verify-repo", str(repo),
                "--public-key", str(self.public_key),
                "--print-package",
            )
            self.assertEqual(verify.returncode, 0, msg=verify.stderr)

            deb_path = repo / "omb_0.2.0_amd64.deb"
            self.assertTrue(deb_path.is_file())
            expected_sha = sha256_bytes(deb_path.read_bytes())
            self.assertEqual(
                verify.stdout.strip(),
                f"omb_0.2.0_amd64.deb\t{expected_sha}\t0.2.0\t{COMMIT_SHA}\tstable",
            )

            repo_manifest = json.loads((repo / "repo-manifest.json").read_text(encoding="utf-8"))
            self.assertEqual(repo_manifest["format"], 2)
            self.assertEqual(repo_manifest["package"]["version"], "0.2.0")
            self.assertEqual(repo_manifest["package"]["channel"], "stable")
            self.assertNotIn("from_commit", repo_manifest["package"])
            self.assertIn("packages_index", repo_manifest)
            self.assertIn("system_debs", repo_manifest)
            self.assertIn("dependencies", repo_manifest)

            extracted = root / "extracted"
            self.extract_deb(deb_path, extracted)

            for binary in BINARIES:
                self.assertTrue((extracted / "usr/bin" / binary).is_file(), msg=f"missing /usr/bin/{binary}")
            self.assertTrue((extracted / "usr/bin/omb").is_file())
            self.assertTrue((extracted / "usr/bin/omb-setup").is_file())

            mig_manifest = json.loads(
                (extracted / "usr/share/omb/migrations/manifest.json").read_text(encoding="utf-8")
            )
            self.assertEqual(mig_manifest, {"format": 1, "migrations": []})

            release_json = json.loads(
                (extracted / "usr/share/omb/release.json").read_text(encoding="utf-8")
            )
            self.assertEqual(release_json["version"], "0.2.0")
            self.assertEqual(release_json["display_version"], "v0.2.0")
            self.assertEqual(release_json["channel"], "stable")
            self.assertEqual(release_json["commit"], COMMIT_SHA)
            self.assertEqual(release_json["package"], "omb 0.2.0")
            self.assertNotIn("from_commit", release_json)
            self.assertRegex(release_json["source_repo"], r"^[^/]+/[^/]+$")

            for sql_name in ("boot.sql", "images.sql", "control.sql"):
                self.assertTrue((extracted / "usr/share/omb/bootstrap" / sql_name).is_file())

    def test_build_with_ordered_cumulative_migrations(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            migrations = {
                "omb_control": {
                    "0002_add_flag.sql": "ALTER TABLE users ADD COLUMN flag BOOLEAN NOT NULL DEFAULT false;\n",
                    "0001_add_note.sql": "ALTER TABLE users ADD COLUMN note TEXT;\n",
                },
                "omb_images": {
                    "0001_add_label.sql": "ALTER TABLE images ADD COLUMN label TEXT;\n",
                },
                "omb_boot": {
                    "0002_boot_idx.sql": "CREATE INDEX idx_boot ON boot_items(id);\n",
                    "0001_boot_col.sql": "ALTER TABLE boot_items ADD COLUMN extra TEXT;\n",
                },
            }
            payload, license_file = self.make_payload(root, migrations=migrations)
            repo = root / "apt-repo"

            build = self.run_cli(
                "build", str(payload), str(repo),
                "--version", "0.3.0~dev.1",
                "--channel", "dev",
                "--commit", COMMIT_SHA,
                "--license", str(license_file),
                "--sign-key", str(self.private_key),
            )
            self.assertEqual(build.returncode, 0, msg=build.stderr)

            extracted = root / "extracted"
            self.extract_deb(repo / "omb_0.3.0~dev.1_amd64.deb", extracted)

            mig_manifest = json.loads(
                (extracted / "usr/share/omb/migrations/manifest.json").read_text(encoding="utf-8")
            )
            self.assertEqual(mig_manifest["format"], 1)
            entries = mig_manifest["migrations"]
            expected_order = [
                ("omb_boot", "0001_boot_col", "omb_boot/0001_boot_col.sql"),
                ("omb_boot", "0002_boot_idx", "omb_boot/0002_boot_idx.sql"),
                ("omb_images", "0001_add_label", "omb_images/0001_add_label.sql"),
                ("omb_control", "0001_add_note", "omb_control/0001_add_note.sql"),
                ("omb_control", "0002_add_flag", "omb_control/0002_add_flag.sql"),
            ]
            self.assertEqual(
                [(e["database"], e["id"], e["path"]) for e in entries],
                expected_order,
            )
            for entry in entries:
                staged = extracted / "usr/share/omb/migrations" / entry["path"]
                self.assertTrue(staged.is_file())
                self.assertEqual(entry["sha256"], sha256_bytes(staged.read_bytes()))

    def test_build_records_explicit_source_repo(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            payload, license_file = self.make_payload(root)
            repo = root / "apt-repo"
            build = self.run_cli(
                "build", str(payload), str(repo),
                "--version", "0.2.0~dev.1",
                "--channel", "dev",
                "--commit", COMMIT_SHA,
                "--license", str(license_file),
                "--sign-key", str(self.private_key),
                "--source-repo", "example-org/omb.fork",
            )
            self.assertEqual(build.returncode, 0, msg=build.stderr)
            deb = next(repo.glob("omb_*.deb"))
            extracted = root / "extracted"
            self.extract_deb(deb, extracted)
            release_json = json.loads((extracted / "usr/share/omb/release.json").read_text(encoding="utf-8"))
            self.assertEqual(release_json["source_repo"], "example-org/omb.fork")

    def test_rejects_invalid_source_repo(self):
        for bad in ("no-slash", "a/b/c", "https://github.com/a/b", "own er/repo", "owner/../x"):
            with self.subTest(bad=bad), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                payload, license_file = self.make_payload(root)
                repo = root / "apt-repo"
                build = self.run_cli(
                    "build", str(payload), str(repo),
                    "--version", "0.2.0",
                    "--channel", "stable",
                    "--commit", COMMIT_SHA,
                    "--license", str(license_file),
                    "--sign-key", str(self.private_key),
                    "--source-repo", bad,
                )
                self.assertNotEqual(build.returncode, 0)
                self.assertIn("owner/repo", build.stderr.lower())
                self.assertFalse(repo.exists())

    def test_rejects_invalid_migration_filename(self):
        for bad_name in ("2026-11-24-foo.sql", "01_foo.sql"):
            with self.subTest(bad_name=bad_name), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                payload, license_file = self.make_payload(
                    root,
                    migrations={"omb_control": {bad_name: "SELECT 1;\n"}},
                )
                repo = root / "apt-repo"
                build = self.run_cli(
                    "build", str(payload), str(repo),
                    "--version", "0.2.0",
                    "--channel", "stable",
                    "--commit", COMMIT_SHA,
                    "--license", str(license_file),
                    "--sign-key", str(self.private_key),
                )
                self.assertNotEqual(build.returncode, 0)
                self.assertIn("invalid migration filename", build.stderr.lower())
                self.assertFalse(repo.exists())

    def test_rejects_transaction_statements_in_migration(self):
        forbidden_cases = [
            "BEGIN;\nSELECT 1;\n",
            "SELECT 1;\nCOMMIT;\n",
            "ROLLBACK;\n",
            "VACUUM;\n",
            "begin;\n",
            "DO $$ BEGIN PERFORM 1; END $$;\nCOMMIT;\n",
        ]
        for sql in forbidden_cases:
            with self.subTest(sql=sql.strip()), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                payload, license_file = self.make_payload(
                    root,
                    migrations={"omb_control": {"0001_bad.sql": sql}},
                )
                repo = root / "apt-repo"
                build = self.run_cli(
                    "build", str(payload), str(repo),
                    "--version", "0.2.0",
                    "--channel", "stable",
                    "--commit", COMMIT_SHA,
                    "--license", str(license_file),
                    "--sign-key", str(self.private_key),
                )
                self.assertNotEqual(build.returncode, 0)
                self.assertIn("forbidden", build.stderr.lower())
                self.assertFalse(repo.exists())

        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            safe_sql = (
                "-- BEGIN comment with ROLLBACK and VACUUM\n"
                "/* COMMIT inside block comment */\n"
                "INSERT INTO notes (body) VALUES ('BEGIN'), ('COMMIT'), ('it''s a ROLLBACK');\n"
                'ALTER TABLE users ADD COLUMN "begin" TEXT;\n'
                "DO $$ BEGIN PERFORM 1; END $$;\n"
                "CREATE OR REPLACE FUNCTION touch_row() RETURNS void LANGUAGE plpgsql AS $fn$\n"
                "BEGIN\n"
                "    PERFORM 1;\n"
                "END;\n"
                "$fn$;\n"
            )
            payload, license_file = self.make_payload(
                root,
                migrations={"omb_control": {"0001_safe.sql": safe_sql}},
            )
            repo = root / "apt-repo"
            build = self.run_cli(
                "build", str(payload), str(repo),
                "--version", "0.2.0",
                "--channel", "stable",
                "--commit", COMMIT_SHA,
                "--license", str(license_file),
                "--sign-key", str(self.private_key),
            )
            self.assertEqual(build.returncode, 0, msg=build.stderr)

    def test_rejects_missing_explicit_metadata_paths(self):
        for flag in ("--go-mod", "--unattend-upstream"):
            with self.subTest(flag=flag), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                payload, license_file = self.make_payload(root)
                repo = root / "apt-repo"
                missing = root / "does-not-exist"
                build = self.run_cli(
                    "build", str(payload), str(repo),
                    "--version", "0.2.0",
                    "--channel", "stable",
                    "--commit", COMMIT_SHA,
                    "--license", str(license_file),
                    "--sign-key", str(self.private_key),
                    flag, str(missing),
                )
                self.assertNotEqual(build.returncode, 0)
                self.assertIn("missing required regular file", build.stderr.lower())
                self.assertFalse(repo.exists())

    def test_verify_repo_rejects_tampered_deb_and_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            payload, license_file = self.make_payload(root)
            repo = root / "apt-repo"

            build = self.run_cli(
                "build", str(payload), str(repo),
                "--version", "0.2.0",
                "--channel", "stable",
                "--commit", COMMIT_SHA,
                "--license", str(license_file),
                "--sign-key", str(self.private_key),
            )
            self.assertEqual(build.returncode, 0, msg=build.stderr)

            deb_path = repo / "omb_0.2.0_amd64.deb"
            original_deb = deb_path.read_bytes()
            tampered_deb = bytearray(original_deb)
            tampered_deb[-1] ^= 0xFF
            deb_path.write_bytes(bytes(tampered_deb))

            bad_deb = self.run_cli(
                "verify-repo", str(repo),
                "--public-key", str(self.public_key),
            )
            self.assertNotEqual(bad_deb.returncode, 0)

            deb_path.write_bytes(original_deb)
            manifest_path = repo / "repo-manifest.json"
            original_manifest = manifest_path.read_bytes()
            manifest_path.write_bytes(original_manifest + b" ")

            bad_manifest = self.run_cli(
                "verify-repo", str(repo),
                "--public-key", str(self.public_key),
            )
            self.assertNotEqual(bad_manifest.returncode, 0)


if __name__ == "__main__":
    unittest.main()
