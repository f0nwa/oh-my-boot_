from pathlib import Path
import gzip
import json
import runpy
import subprocess
import tempfile
import unittest


class OfflineDebTests(unittest.TestCase):
    def test_units_use_package_paths_and_agent_helper(self):
        builder = runpy.run_path(str(Path(__file__).with_name("offline-deb.py")))
        unit = "[Service]\nExecStart=/usr/local/bin/omb-agent\n"
        self.assertEqual(builder["package_unit"]("omb-agent.service", unit),
                         "[Service]\nExecStart=/usr/bin/omb-agent "
                         "-ubuntu-source-script /usr/lib/omb/omb-prepare-ubuntu-source\n")
        self.assertEqual(builder["package_unit"]("omb-dnsmasq.service", unit.replace(
            "/usr/local/bin/omb-agent", "/usr/sbin/dnsmasq")),
            "[Service]\nExecStart=/usr/sbin/dnsmasq\n")

    def test_rejects_wrong_binary_architecture(self):
        builder = runpy.run_path(str(Path(__file__).with_name("offline-deb.py")))
        with tempfile.TemporaryDirectory() as directory:
            binary = Path(directory) / "omb-control"
            binary.write_bytes(b"\x7fELF\x02\x01" + bytes(12) + (183).to_bytes(2, "little"))
            with self.assertRaisesRegex(ValueError, "amd64"):
                builder["require_amd64_elf"](binary)

    def test_update_overlay_accepts_units_and_helpers_and_rejects_provisioning_inputs(self):
        builder = runpy.run_path(str(Path(__file__).with_name("offline-deb.py")))
        amd64_elf = b"\x7fELF\x02\x01" + bytes(12) + (62).to_bytes(2, "little") + b"ubuntu-source-script"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            base = root / "base"
            update = root / "update"
            package = root / "pkg"
            license_path = root / "LICENSE"
            license_path.write_text("MIT\n")
            for rel in (
                "bin/omb-agent", "bin/omb-boot", "bin/omb-control",
                "assets/ipxeboot.tar.gz", "assets/ipxe-shimx64.efi", "assets/wimboot",
                "deploy/samba/smb.conf",
                "deploy/boot-assets/windows-profiles/winpeshl.ini",
                "deploy/sql/control.sql",
            ):
                p = base / rel
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_bytes(amd64_elf if rel.startswith("bin/") else b"base\n")
            for svc in ("omb-agent", "omb-boot", "omb-control", "omb-dnsmasq", "omb-images", "omb-winiso@"):
                p = base / "deploy/systemd" / f"{svc}.service"
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text(f"[Service]\nExecStart=/usr/local/bin/{svc}\n")
            for helper in ("scripts/prepare-ubuntu-source.sh", "scripts/fetch-ipxe.sh"):
                p = base / helper
                p.parent.mkdir(parents=True, exist_ok=True)
                p.write_text("#!/bin/sh\necho base\n")

            (update / "deploy/systemd").mkdir(parents=True)
            (update / "deploy/systemd/omb-agent.service").write_text(
                "[Unit]\nDescription=Updated\n[Service]\nExecStart=/usr/local/bin/omb-agent\n"
            )
            (update / "scripts").mkdir(parents=True)
            (update / "scripts/prepare-ubuntu-source.sh").write_text("#!/bin/sh\necho updated\n")

            base_manifest = {
                "commit": "a" * 40,
                "from_commit": None,
                "migrations": [{"database": "omb_control", "path": "deploy/sql/control.sql"}],
                "files": {},
            }
            update_manifest = {
                "commit": "b" * 40,
                "from_commit": "a" * 40,
                "migrations": [],
                "files": {
                    "deploy/systemd/omb-agent.service": {},
                    "scripts/prepare-ubuntu-source.sh": {},
                },
            }
            builder["stage_package_tree"](package, base, base_manifest, "0.1~test2", license_path,
                                          update=update, update_manifest=update_manifest)
            agent_unit = (package / "usr/lib/systemd/system/omb-agent.service").read_text()
            self.assertIn("Description=Updated", agent_unit)
            self.assertIn("ExecStart=/usr/bin/omb-agent -ubuntu-source-script /usr/lib/omb/omb-prepare-ubuntu-source",
                          agent_unit)
            self.assertIn("echo updated", (package / "usr/lib/omb/omb-prepare-ubuntu-source").read_text())
            self.assertIn("echo base", (package / "usr/lib/omb/fetch-ipxe").read_text())

            for forbidden in ("assets/wimboot", "deploy/samba/smb.conf",
                              "deploy/boot-assets/windows-profiles/winpeshl.ini"):
                bad_manifest = {
                    "commit": "b" * 40,
                    "from_commit": "a" * 40,
                    "migrations": [],
                    "files": {forbidden: {}},
                }
                with self.assertRaisesRegex(ValueError, "unsupported update payload"):
                    builder["validate_update_payload"](bad_manifest)

    def test_dependency_parsers_and_signed_repo_verification(self):
        builder = runpy.run_path(str(Path(__file__).with_name("offline-deb.py")))
        repo_root = Path(__file__).resolve().parent.parent
        go_info = builder["parse_go_mod"](repo_root / "go.mod")
        self.assertEqual(go_info["version"], "1.26.0")
        self.assertTrue(any(m["path"] == "github.com/jackc/pgx/v5" and not m["indirect"]
                            for m in go_info["modules"]))
        self.assertTrue(any(m["path"] == "golang.org/x/sync" and m["indirect"]
                            for m in go_info["modules"]))
        upstream = builder["parse_unattend_upstream"](repo_root / "internal/unattend/UPSTREAM.md")
        self.assertEqual(upstream["repo"], "cschneegans/unattend-generator")
        self.assertEqual(len(upstream["commit"]), 40)

        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            repo = root / "apt-repo"
            repo.mkdir()
            priv = root / "priv.pem"
            pub = root / "pub.pem"
            subprocess.run(["openssl", "genpkey", "-algorithm", "RSA", "-out", str(priv)],
                           check=True, capture_output=True)
            subprocess.run(["openssl", "pkey", "-in", str(priv), "-pubout", "-out", str(pub)],
                           check=True, capture_output=True)

            sys_deb = repo / "curl_8.0_amd64.deb"
            sys_deb.write_bytes(b"fake-curl-deb")
            omb_deb = repo / "omb_0.1~test1_amd64.deb"
            omb_deb.write_bytes(b"fake-omb-deb")
            idx = repo / "Packages.gz"
            idx.write_bytes(gzip.compress(
                f"Package: curl\nVersion: 8.0\nArchitecture: amd64\n"
                f"Filename: ./{sys_deb.name}\nSize: {sys_deb.stat().st_size}\n"
                f"SHA256: {builder['file_digest'](sys_deb, 'sha256')}\n".encode(),
                mtime=0,
            ))
            parsed = builder["parse_packages_index"](idx.read_bytes())
            self.assertEqual(len(parsed), 1)
            self.assertEqual(parsed[0]["package"], "curl")

            manifest = {
                "format": 1,
                "target": builder["TARGET"],
                "apt_index": {
                    "filename": "Packages.gz",
                    "size": idx.stat().st_size,
                    "sha256": builder["file_digest"](idx, "sha256"),
                },
                "packages": [{
                    "package": "omb",
                    "version": "0.1~test1",
                    "architecture": "amd64",
                    "commit": "a" * 40,
                    "from_commit": None,
                    "filename": omb_deb.name,
                    "size": omb_deb.stat().st_size,
                    "sha256": builder["file_digest"](omb_deb, "sha256"),
                }],
                "dependencies": {
                    "system_debs": parsed,
                    "boot_assets": {},
                    "go": go_info,
                    "unattend_upstream": upstream,
                },
            }
            raw = (json.dumps(manifest, sort_keys=True, indent=2) + "\n").encode()
            (repo / "repo-manifest.json").write_bytes(raw)
            (repo / "repo-manifest.sig").write_bytes(builder["openssl_signature"]("-sign", raw, priv))

            entry = builder["verify_repository"](repo, pub, version="0.1~test1")
            self.assertEqual(entry["filename"], omb_deb.name)
            self.assertEqual(entry["commit"], "a" * 40)

            with self.assertRaisesRegex(ValueError, "version not found"):
                builder["verify_repository"](repo, pub, version="9.9")

            # Extra unmanifested .deb must be rejected.
            extra = repo / "rogue_1.0_amd64.deb"
            extra.write_bytes(b"rogue")
            with self.assertRaisesRegex(ValueError, "unexpected .deb"):
                builder["verify_repository"](repo, pub)
            extra.unlink()

            # Tampering with a packaged .deb must be rejected.
            omb_deb.write_bytes(b"tampered")
            with self.assertRaisesRegex(ValueError, "checksum or size mismatch"):
                builder["verify_repository"](repo, pub)
            omb_deb.write_bytes(b"fake-omb-deb")

            # Wrong public key must be rejected.
            priv2 = root / "priv2.pem"
            pub2 = root / "pub2.pem"
            subprocess.run(["openssl", "genpkey", "-algorithm", "RSA", "-out", str(priv2)],
                           check=True, capture_output=True)
            subprocess.run(["openssl", "pkey", "-in", str(priv2), "-pubout", "-out", str(pub2)],
                           check=True, capture_output=True)
            with self.assertRaisesRegex(ValueError, "signature"):
                builder["verify_repository"](repo, pub2)
