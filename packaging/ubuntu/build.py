#!/usr/bin/env python3
"""Build a native Ubuntu 24.04 exporter package in a temporary staging tree."""

import argparse
from datetime import datetime, timezone
from email.utils import format_datetime
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


HERE = Path(__file__).resolve().parent
REPO = HERE.parents[1]
NAME = "hyperliquid-exporter"
PACKAGE = "hyperliquid-metrics-exporter"


def capture(*args, cwd=REPO):
    return subprocess.check_output(args, cwd=cwd, text=True).strip()


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def install(source, destination, mode=0o644):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(mode)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, help="package a prebuilt native ELF instead of compiling")
    parser.add_argument("--version", help="Debian version, e.g. 2.5.1-1; required with --binary")
    parser.add_argument("--maintainer", default="Erik Lonroth <erik@dwellir.com>")
    parser.add_argument("--output", type=Path, default=REPO / "dist/ubuntu")
    parser.add_argument("--jobs", type=int, default=4)
    args = parser.parse_args()
    if not re.fullmatch(r"[^<>\r\n]+ <[^<>\s]+@[^<>\s]+>", args.maintainer):
        parser.error("maintainer must be Name <email@example.com>")
    if args.jobs < 1:
        parser.error("jobs must be positive")
    release = dict(line.split("=", 1) for line in Path("/etc/os-release").read_text().splitlines() if "=" in line)
    if release.get("ID", "").strip('"') != "ubuntu" or release.get("VERSION_ID", "").strip('"') != "24.04":
        parser.error("build on Ubuntu 24.04 so runtime dependencies match the target")
    arch = capture("dpkg", "--print-architecture")
    if arch not in ("amd64", "arm64"):
        parser.error("supported native architectures are amd64 and arm64")
    for tool in ("dh", "dpkg-buildpackage", "readelf", "fakeroot"):
        if not shutil.which(tool):
            parser.error(f"missing build prerequisite: {tool}")
    if args.binary and not args.version:
        parser.error("--version is required with --binary")
    version = args.version or (REPO / "VERSION").read_text().strip().removeprefix("v") + "-1"
    if not re.fullmatch(r"[0-9][0-9A-Za-z.+~]*-[0-9A-Za-z.+~]+", version):
        parser.error("version must be a Debian upstream-version-revision without paths or whitespace")
    subprocess.run(["dpkg", "--validate-version", version], check=True)
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    artifact = output / f"{PACKAGE}_{version}_{arch}.deb"
    outputs = [artifact, artifact.with_suffix(".deb.sha256"), artifact.with_suffix(".deb.build-info.json")]
    for path in outputs:
        if path.exists():
            parser.error(f"output already exists: {path}; use a new revision or output directory")
    commit = capture("git", "rev-parse", "HEAD")
    epoch = int(os.environ.get("SOURCE_DATE_EPOCH") or capture("git", "show", "-s", "--format=%ct", "HEAD"))
    provenance = {"package": PACKAGE, "version": version, "architecture": arch,
                  "distribution": "ubuntu/noble", "build_mode": "prebuilt" if args.binary else "source",
                  "source_commit": commit,
                  "source_dirty": bool(capture("git", "status", "--porcelain", "--untracked-files=all")),
                  "source_date_epoch": epoch}
    env = dict(os.environ, SOURCE_DATE_EPOCH=str(epoch), LC_ALL="C.UTF-8")
    with tempfile.TemporaryDirectory(prefix="exporter-deb-") as temporary:
        work = Path(temporary)
        binary = args.binary.resolve() if args.binary else work / NAME
        if not args.binary:
            toolchain = re.search(r"^go (\S+)$", (REPO / "go.mod").read_text(), re.M)[1]
            env.update(GOTOOLCHAIN="go" + toolchain, CGO_ENABLED="0", GOOS="linux", GOARCH=arch, GOAMD64="v1")
            label = version.rsplit("-", 1)[0].replace("~", "-")
            build_time = datetime.fromtimestamp(epoch, timezone.utc).strftime("%Y-%m-%d_%H:%M:%S")
            flags = f"-X main.version={label} -X main.commit={commit} -X main.buildTimeUTC={build_time}"
            subprocess.run(["go", "build", "-mod=readonly", "-trimpath", f"-p={args.jobs}",
                            "-ldflags", flags, "-o", str(binary), "./cmd/hyperliquid-exporter"],
                           cwd=REPO, env=env, check=True)
            provenance["go_version"] = subprocess.check_output(["go", "version"], env=env, text=True).strip()
        header = capture("readelf", "-h", str(binary))
        expected = "Advanced Micro Devices X86-64" if arch == "amd64" else "AArch64"
        if expected not in header:
            parser.error(f"binary architecture does not match {arch}")
        provenance["input_binary_sha256"] = digest(binary)
        package_root = work / "build"
        debian = package_root / "debian"
        debian.mkdir(parents=True)
        payload = package_root / "payload"
        install(binary, payload / "usr/bin" / NAME, 0o755)
        install(HERE / "service", payload / "usr/lib/systemd/system" / f"{NAME}.service")
        install(HERE / "config", payload / "etc/default" / NAME, 0o600)
        doc = payload / "usr/share/doc" / PACKAGE
        install(REPO / "LICENSE", doc / "copyright")
        install(HERE / "README.md", doc / "README.packaging")
        install(HERE / "charm-integration.md", doc / "charm-integration.md")
        doc.joinpath("build-info.json").write_text(json.dumps(provenance, indent=2) + "\n")
        replacements = {"@PACKAGE@": PACKAGE, "@SERVICE@": NAME,
                        "@VERSION@": version, "@MAINTAINER@": args.maintainer,
                        "@DATE@": format_datetime(datetime.fromtimestamp(epoch, timezone.utc))}
        for template in (HERE / "debian").iterdir():
            content = template.read_text()
            for marker, value in replacements.items():
                content = content.replace(marker, value)
            destination = debian / template.name
            destination.write_text(content)
            destination.chmod(0o755 if template.name in ("rules", "postinst", "prerm") else 0o644)
        subprocess.run(["dpkg-buildpackage", "--build=binary", "--no-sign"], cwd=package_root, env=env, check=True)
        shutil.copyfile(work / artifact.name, artifact)
    outputs[1].write_text(f"{digest(artifact)}  {artifact.name}\n")
    outputs[2].write_text(json.dumps(provenance, indent=2) + "\n")
    print(f"Built {artifact}")


if __name__ == "__main__":
    main()
