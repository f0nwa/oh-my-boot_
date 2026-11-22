import importlib.util
import io
import json
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch


TOOL = Path(__file__).with_name("offline-release.py")
A = "a" * 40
B = "b" * 40


class OfflineReleaseTests(unittest.TestCase):
    def test_signed_update_is_prepared_and_tampering_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            payload = root / "payload"
            (payload / "bin").mkdir(parents=True)
            (payload / "bin/omb-control").write_bytes(b"control")
            (payload / "deploy/sql").mkdir(parents=True)
            migration = "deploy/sql/change.sql"
            (payload / migration).write_text("SELECT 1;")
            key = root / "private.pem"
            public_key = root / "public.pem"
            self.openssl("genpkey", "-algorithm", "RSA", "-out", key)
            self.openssl("pkey", "-in", key, "-pubout", "-out", public_key)
            archive = root / "update.tar.gz"
            self.invoke("pack", payload, archive, "--commit", B, "--from-commit", A,
                        "--migration", f"omb_control={migration}", "--sign-key", key)
            extracted = root / "staging"
            self.invoke("verify", archive, "--public-key", public_key, "--expect-from", A,
                        "--extract", extracted)
            manifest = json.loads((extracted / "manifest.json").read_bytes())
            self.assertEqual(manifest["commit"], B)
            self.assertEqual(manifest["migrations"], [{"database": "omb_control", "path": migration}])
            self.assertEqual((extracted / "bin/omb-control").read_bytes(), b"control")
            self.invoke("verify", archive, "--public-key", public_key,
                        "--expect-from", "c" * 40, error="source commit")

            bad = root / "changed.tar.gz"
            self.copy_with_change(archive, bad, "bin/omb-control", b"changed")
            rejected = root / "rejected"
            self.invoke("verify", bad, "--public-key", public_key, "--extract", rejected,
                        error="checksum")
            self.assertFalse(rejected.exists())

            self.copy_with_change(archive, bad, "../out", b"extra", add=True)
            self.invoke("verify", bad, "--public-key", public_key, error="member")
            self.assertFalse((root / "out").exists())

            self.copy_with_change(archive, bad, "new.txt", b"extra", add=True)
            self.invoke("verify", bad, "--public-key", public_key, error="member")

            self.openssl("genpkey", "-algorithm", "RSA", "-out", key)
            self.openssl("pkey", "-in", key, "-pubout", "-out", public_key)
            self.invoke("verify", archive, "--public-key", public_key, error="signature")

    def test_preflight_rejects_an_existing_install_or_wrong_source(self):
        spec = importlib.util.spec_from_file_location("omb_release", TOOL)
        release = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(release)
        real_read = Path.read_text
        real_exists = Path.exists

        def read_text(path, *args, **kwargs):
            if path == Path("/etc/os-release"):
                return 'ID=ubuntu\nVERSION_ID="26.04"\n'
            return real_read(path, *args, **kwargs)

        def exists(path):
            return path != Path("/usr/local/bin/omb-control") and real_exists(path)

        with tempfile.TemporaryDirectory() as tmp, patch.object(Path, "read_text", read_text), \
                patch.object(Path, "exists", exists):
            state = Path(tmp) / "COMMIT"
            with patch.object(release, "STATE", state), patch.object(release.platform, "machine", return_value="x86_64"):
                release.check_host({"from_commit": None})
                state.write_text(A)
                release.check_host({"from_commit": A})
                for other in (None, B):
                    with self.assertRaisesRegex(ValueError, "source commit"):
                        release.check_host({"from_commit": other})

    def test_update_allows_binary_unit_or_helper_without_migrations_and_rejects_empty(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            key = root / "private.pem"
            public_key = root / "public.pem"
            self.openssl("genpkey", "-algorithm", "RSA", "-out", key)
            self.openssl("pkey", "-in", key, "-pubout", "-out", public_key)

            bin_only = root / "bin-only"
            (bin_only / "bin").mkdir(parents=True)
            (bin_only / "bin/omb-boot").write_bytes(b"boot")
            archive = root / "bin-only.tar.gz"
            self.invoke("pack", bin_only, archive, "--commit", B, "--from-commit", A, "--sign-key", key)
            self.invoke("verify", archive, "--public-key", public_key, "--expect-from", A)

            unit_only = root / "unit-only"
            (unit_only / "deploy/systemd").mkdir(parents=True)
            (unit_only / "deploy/systemd/omb-agent.service").write_text("[Service]\nExecStart=/usr/local/bin/omb-agent\n")
            (unit_only / "scripts").mkdir(parents=True)
            (unit_only / "scripts/prepare-ubuntu-source.sh").write_text("#!/bin/sh\n")
            unit_archive = root / "unit-only.tar.gz"
            self.invoke("pack", unit_only, unit_archive, "--commit", B, "--from-commit", A, "--sign-key", key)
            self.invoke("verify", unit_archive, "--public-key", public_key, "--expect-from", A)

            readme_only = root / "readme-only"
            readme_only.mkdir()
            (readme_only / "README.md").write_text("notes\n")
            self.invoke("pack", readme_only, root / "readme.tar.gz",
                        "--commit", B, "--from-commit", A, "--sign-key", key,
                        error="at least one binary, unit, helper, or migration")

    def openssl(self, *args):
        subprocess.run(["openssl", *map(str, args)], check=True, capture_output=True)

    def invoke(self, *args, error=None):
        result = subprocess.run([sys.executable, str(TOOL), *map(str, args)],
                                capture_output=True, text=True)
        if error:
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(error, result.stderr)
        else:
            self.assertEqual(result.returncode, 0, result.stderr)

    def copy_with_change(self, source, target, name, content, add=False):
        with tarfile.open(source, "r:gz") as src, tarfile.open(target, "w:gz") as dst:
            for member in src:
                data = src.extractfile(member).read()
                if member.name == name:
                    data = content
                    member.size = len(data)
                dst.addfile(member, io.BytesIO(data))
            if add:
                member = tarfile.TarInfo(name)
                member.size = len(content)
                dst.addfile(member, io.BytesIO(content))


if __name__ == "__main__":
    unittest.main()
