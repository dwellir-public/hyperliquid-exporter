"""Exercise real Debian archives without compiling the exporter."""

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


PACKAGING = Path(__file__).resolve().parents[1]
NAME = "hyperliquid-exporter"


class PackageTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="exporter-package-test-")
        cls.root = Path(cls.temp.name)
        cls.binary = cls.root / "fixture"
        subprocess.run(["cc", "-x", "c", "-o", str(cls.binary), "-"],
                       input='#include <stdio.h>\nint main(void) { puts("fixture"); return 0; }\n',
                       text=True, check=True)

    @classmethod
    def tearDownClass(cls):
        cls.temp.cleanup()

    def build(self, version="1.2.3-1", output=None, binary=None, maintainer=None):
        command = [sys.executable, str(PACKAGING / "build.py"),
                   "--binary", str(binary or self.binary), "--version", version,
                   "--output", str(output or self.root / self._testMethodName)]
        if maintainer is not None:
            command += ["--maintainer", maintainer]
        return subprocess.run(command, text=True, capture_output=True)

    def test_archive_and_lifecycle(self):
        result = self.build()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        archive = next((self.root / self._testMethodName).glob("*.deb"))
        control = subprocess.check_output(["dpkg-deb", "-f", str(archive)], text=True)
        for field in (f"Package: {NAME}\n", "Version: 1.2.3-1\n", "libc6", "adduser", "ca-certificates"):
            self.assertIn(field, control)
        unpacked = self.root / "unpacked"
        subprocess.run(["dpkg-deb", "-R", str(archive), str(unpacked)], check=True)
        with tempfile.TemporaryFile() as stream:
            subprocess.run(["dpkg-deb", "--fsys-tarfile", str(archive)], stdout=stream, check=True)
            stream.seek(0)
            with tarfile.open(fileobj=stream) as payload:
                self.assertTrue(all(m.uid == 0 and m.gid == 0 for m in payload.getmembers()))
        config = unpacked / "etc" / NAME / f"{NAME}.conf"
        self.assertEqual(config.stat().st_mode & 0o777, 0o600)
        self.assertIn(f"/etc/{NAME}/{NAME}.conf", (unpacked / "DEBIAN/conffiles").read_text())
        unit = (unpacked / "usr/lib/systemd/system" / f"{NAME}.service").read_text()
        for setting in (f"User={NAME}", f"Group={NAME}", "ProtectSystem=strict",
                        "ProtectHome=read-only", "NoNewPrivileges=yes", "CapabilityBoundingSet=\n"):
            self.assertIn(setting, unit)
        self.assertEqual(subprocess.check_output([str(unpacked / "usr/bin" / NAME)], text=True).strip(), "fixture")
        postinst = (unpacked / "DEBIAN/postinst").read_text()
        self.assertIn("try-restart", postinst)
        self.assertNotIn("deb-systemd-invoke start", postinst)
        self.assertIn("/usr/sbin/nologin", postinst)
        self.assertIn("stop", (unpacked / "DEBIAN/prerm").read_text())
        for script in ("postinst", "prerm", "postrm"):
            subprocess.run(["sh", "-n", str(unpacked / "DEBIAN" / script)], check=True)
        provenance = json.loads(archive.with_suffix(".deb.build-info.json").read_text())
        self.assertEqual(provenance["build_mode"], "prebuilt")
        self.assertEqual(provenance["input_binary_sha256"], hashlib.sha256(self.binary.read_bytes()).hexdigest())
        self.assertEqual(provenance, json.loads((unpacked / "usr/share/doc" / NAME / "build-info.json").read_text()))
        self.assertEqual(archive.with_suffix(".deb.sha256").read_text(),
                         f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
        original = archive.read_bytes()
        duplicate = self.build()
        self.assertNotEqual(duplicate.returncode, 0)
        self.assertIn("already exists", duplicate.stderr)
        self.assertEqual(archive.read_bytes(), original)

    def test_rejects_invalid_version(self):
        result = self.build(version="../../bad")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("version", result.stderr.lower())

    def test_rejects_control_field_injection(self):
        result = self.build(maintainer="Test <test@example.invalid>\nDepends: bad")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("maintainer", result.stderr.lower())

    def test_rejects_wrong_architecture(self):
        binary = self.root / "wrong-arch"
        data = bytearray(self.binary.read_bytes())
        # ELF e_machine at offset 18, in the native fixture's byte order.
        data[18:20] = (183 if data[18:20] == b'\x3e\x00' else 62).to_bytes(2, "little")
        binary.write_bytes(data)
        result = self.build(binary=binary)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("architecture", result.stderr.lower())


if __name__ == "__main__":
    unittest.main()
