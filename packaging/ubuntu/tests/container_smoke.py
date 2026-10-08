#!/usr/bin/env python3
"""Destructive package lifecycle checks for a fresh disposable systemd container."""

import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request


NAME = "hyperliquid-exporter"
STATE = Path("/var/lib") / NAME
CONFIG = Path("/etc") / NAME / f"{NAME}.conf"


def run(*args):
    subprocess.run(args, check=True)


def capture(*args):
    return subprocess.check_output(args, text=True).strip()


def active():
    return subprocess.run(["systemctl", "is-active", "--quiet", NAME]).returncode == 0


def install(package):
    run("apt-get", "install", "--reinstall", "-y", "-o", "Dpkg::Options::=--force-confold", str(package))


def wait_active():
    for _ in range(100):
        if active() and (STATE / "probe-ok").exists():
            return
        time.sleep(0.1)
    raise AssertionError("service probe did not start successfully")


def main():
    if os.geteuid() != 0 or not Path("/run/systemd/system").exists() or len(sys.argv) != 3:
        sys.exit("Run as root in a fresh disposable Ubuntu systemd container with OLD.deb NEW.deb")
    if CONFIG.exists() or STATE.exists() or subprocess.run(["getent", "passwd", NAME], stdout=subprocess.DEVNULL).returncode == 0:
        sys.exit("Refusing a container with existing exporter configuration, state or account")
    old, new = (Path(arg).resolve() for arg in sys.argv[1:])
    old_version = capture("dpkg-deb", "-f", str(old), "Version")
    new_version = capture("dpkg-deb", "-f", str(new), "Version")
    run("dpkg", "--compare-versions", old_version, "lt", new_version)
    install(old)
    assert not active()
    enabled = subprocess.run(["systemctl", "is-enabled", NAME], text=True, capture_output=True)
    assert enabled.stdout.strip() == "disabled", enabled.stdout + enabled.stderr
    entry = capture("getent", "passwd", NAME).split(":")
    assert 0 < int(entry[2]) < 1000 and entry[5] == str(STATE) and entry[6] == "/usr/sbin/nologin"
    assert capture("id", "-Gn", NAME) == NAME
    assert CONFIG.stat().st_mode & 0o777 == 0o600
    assert CONFIG.stat().st_uid == 0
    run("systemd-analyze", "verify", f"/usr/lib/systemd/system/{NAME}.service")
    run("install", "-d", "-o", NAME, "-g", NAME, "-m", "0700", str(STATE))
    node = Path("/home/node-fixture/hl")
    node.mkdir(parents=True)
    sample = node / "sample"
    sample.write_text("node data\n")
    (STATE / "hl").symlink_to(node)
    config = CONFIG.read_text() + "\n# operator edit retained by upgrades\n"
    CONFIG.write_text(config)
    # Run the real exporter against fixture files, then verify /metrics.
    CONFIG.write_text(config)
    run("systemctl", "start", NAME)
    for _ in range(100):
        try:
            with urllib.request.urlopen("http://127.0.0.1:8086/metrics", timeout=1) as response:
                metrics = response.read()
                assert b"hl_exporter" in metrics
                expected = f"hl_node_disk_used_bytes {sample.stat().st_size}".encode()
                if expected not in metrics:
                    time.sleep(0.1)
                    continue
            break
        except OSError:
            time.sleep(0.1)
    else:
        raise AssertionError("exporter did not serve metrics")
    run("systemctl", "stop", NAME)
    # Replace only ExecStart to exercise the unit's actual filesystem restrictions.
    probe = Path("/usr/local/lib/exporter-permission-probe")
    probe.parent.mkdir(parents=True, exist_ok=True)
    probe.write_text("#!/bin/sh\nset -eu\n"
                     f"cat {STATE}/hl/sample >/dev/null\n"
                     f"if touch {STATE}/hl/forbidden; then exit 1; fi\n"
                     "if touch /usr/bin/exporter-forbidden; then exit 1; fi\n"
                     f"if cat {CONFIG} >/dev/null 2>&1; then exit 1; fi\n"
                     f"touch {STATE}/probe-ok\nexec sleep infinity\n")
    probe.chmod(0o755)
    dropin = Path(f"/etc/systemd/system/{NAME}.service.d/probe.conf")
    dropin.parent.mkdir(parents=True)
    dropin.write_text(f"[Service]\nExecStart=\nExecStart={probe}\n")
    run("systemctl", "daemon-reload")
    run("systemctl", "start", NAME)
    wait_active()
    old_pid = capture("systemctl", "show", "-p", "MainPID", "--value", NAME)
    config = CONFIG.read_text()
    install(new)
    wait_active()
    assert capture("systemctl", "show", "-p", "MainPID", "--value", NAME) != old_pid
    assert CONFIG.read_text() == config
    run("systemctl", "stop", NAME)
    install(new)
    assert not active()
    assert CONFIG.read_text() == config
    run("systemctl", "start", NAME)
    wait_active()
    run("apt-get", "remove", "-y", NAME)
    assert not active()
    assert CONFIG.read_text() == config
    assert (STATE / "hl").is_symlink()
    run("apt-get", "purge", "-y", NAME)
    assert not CONFIG.exists()
    assert capture("id", "-u", NAME) != "0"
    assert (STATE / "probe-ok").exists()
    assert sample.read_text() == "node data\n"
    print("PASS: install, runtime metrics, sandbox, upgrades, remove and purge")


if __name__ == "__main__":
    main()
