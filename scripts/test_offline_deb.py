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
