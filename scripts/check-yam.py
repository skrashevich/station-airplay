#!/usr/bin/env python3
"""Exercise real YAM install/reinstall/doctor/remove on every built platform package."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def check_launcher(yam_source):
    """Run the real launcher from YAM's actual install layout with a host fixture."""
    with tempfile.TemporaryDirectory(prefix="station-airplay-launcher-check-") as tmp:
        work = Path(tmp).resolve()
        source = work / "source"
        source.mkdir()
        fixture = source / "receiver"
        fixture.write_text('#!/bin/sh\nprintf "%s\\n" "$@"\n')
        fixture.chmod(0o755)
        (source / "yam.yaml").write_text(json.dumps({
            "name": "skrashevich/station-airplay", "version": "99.0.0",
            "license": "Apache-2.0", "description": "Launcher integration fixture",
            "targets": ["cucumber"], "files": {
                "bin/station-airplay": str(ROOT / "packaging/yam/launcher.sh"),
                "share/skrashevich.station-airplay/bin/station-airplay": str(fixture),
            },
        }))
        artifact = work / "fixture.yampkg"
        profiles = yam_source / "platforms"
        subprocess.run([str(yam_source / "target/release/yam-publish"), "pack",
                        "--source", str(source), "--profiles", str(profiles),
                        "--platform", "cucumber", "--output", str(artifact)], check=True)
        root = work / "yam root"
        command = [str(yam_source / "target/release/yam"), "--profiles", str(profiles),
                   "--platform", "cucumber", "--root", str(root)]
        subprocess.run([*command, "install", "--yes", str(artifact)], check=True, capture_output=True)
        environment = {key: value for key, value in os.environ.items() if key not in ("YAM_ROOT", "YAM_VAR")}
        result = subprocess.run([str(root / "bin/station-airplay"), "-name", "A name with spaces"],
                                env=environment, check=True, capture_output=True, text=True)
        expected = ["-data-dir", str(root / "var/skrashevich.station-airplay"), "-name", "A name with spaces"]
        if result.stdout.splitlines() != expected:
            raise RuntimeError(f"Installed launcher arguments: {result.stdout.splitlines()!r}; expected {expected!r}")
        assert (root / "var/skrashevich.station-airplay").stat().st_mode & 0o777 == 0o700
        legacy = work / "var/skrashevich.station-airplay"
        legacy.mkdir(parents=True)
        identity = legacy / "identity.json"
        identity.write_text("retained pairing identity")
        result = subprocess.run([str(root / "bin/station-airplay"), "-check"],
                                env=environment, check=True, capture_output=True, text=True)
        assert result.stdout.splitlines() == ["-data-dir", str(legacy), "-check"]
        assert identity.read_text() == "retained pairing identity"
        custom = work / "custom var"
        result = subprocess.run([str(root / "bin/station-airplay"), "-check"],
                                env=dict(environment, YAM_VAR=str(custom)), check=True,
                                capture_output=True, text=True)
        assert result.stdout.splitlines() == ["-data-dir", str(custom / "skrashevich.station-airplay"), "-check"]
        print("YAM installed launcher state path and arguments passed")


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--yam-source", type=Path, required=True)
parser.add_argument("--version", required=True)
parser.add_argument("--launcher-only", action="store_true", help="only check the host-executable launcher fixture")
args = parser.parse_args()
yam_source = args.yam_source.resolve()
yam = yam_source / "target/release/yam"
check_launcher(yam_source)
if args.launcher_only:
    sys.exit(0)
artifacts = sorted((ROOT / "dist").glob(f"station-airplay-{args.version}-*.yampkg"))
if not artifacts:
    raise SystemExit("no YAM packages to check")
for artifact in artifacts:
    platform = artifact.name.removeprefix(f"station-airplay-{args.version}-").removesuffix(".yampkg")
    with tempfile.TemporaryDirectory(prefix="station-airplay-yam-check-") as tmp:
        root = Path(tmp) / "yam root"
        command = [str(yam), "--profiles", str(yam_source / "platforms"),
                   "--platform", platform, "--root", str(root)]

        def run(*arguments):
            result = subprocess.run([*command, *arguments], capture_output=True, text=True)
            if result.returncode:
                raise RuntimeError(f"YAM {' '.join(arguments)}: {result.stderr.strip()}")
            return result

        run("install", "--yes", str(artifact))
        run("doctor")
        for binary in (root / "bin/station-airplay",
                       root / "share/skrashevich.station-airplay/bin/station-airplay"):
            if not os.access(binary, os.X_OK):
                raise RuntimeError(f"not executable: {binary}")
        if "skrashevich/station-airplay/receiver" not in run("service", "list").stdout:
            raise RuntimeError("receiver service declaration missing")
        # Mutable pairing state must survive reinstall and package removal.
        state = root / "var/skrashevich.station-airplay/pairing-sentinel"
        state.parent.mkdir(parents=True)
        state.write_text("keep pairing")
        if artifact == artifacts[0]:
            # Exercise an actual version upgrade once; YAM rejects the same version.
            major, minor, patch = args.version.split(".")
            newer = f"{major}.{minor}.{int(patch) + 1}"
            subprocess.run([sys.executable, str(ROOT / "scripts/build-yam.py"),
                            "--version", newer, "--yam-source", str(yam_source),
                            "--platform", platform, "--output-dir", tmp], check=True)
            run("install", "--yes", str(Path(tmp) / f"station-airplay-{newer}-{platform}.yampkg"))
            if newer not in run("list").stdout:
                raise RuntimeError("upgrade did not switch package version")
            run("doctor")
        run("remove", "skrashevich/station-airplay")
        run("install", "--yes", str(artifact))
        run("doctor")
        run("remove", "skrashevich/station-airplay")
        run("doctor")
        if state.read_text() != "keep pairing" or (root / "bin/station-airplay").exists():
            raise RuntimeError("package removal lost state or retained launcher")
        wrong = "mango" if platform != "mango" else "cucumber"
        rejected = subprocess.run([str(yam), "--profiles", str(yam_source / "platforms"),
                                   "--platform", wrong, "--root", str(root),
                                   "install", "--yes", str(artifact)], capture_output=True, text=True)
        if rejected.returncode == 0 or "does not match" not in rejected.stderr:
            raise RuntimeError("wrong-platform installation was not rejected")
    print(f"YAM lifecycle passed: {platform}")
