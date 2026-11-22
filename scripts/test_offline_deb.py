from pathlib import Path
import runpy
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
