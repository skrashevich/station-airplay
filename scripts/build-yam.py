#!/usr/bin/env python3
"""Stage and pack native YAM packages using the pinned upstream yam-publish."""
import argparse
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


def targets():
    return dict(line.split() for line in (ROOT / "packaging/yam/targets.tsv").read_text().splitlines()
                if line.strip() and not line.startswith("#"))


def build(version, yam_source, selected, output=None):
    publisher = yam_source / "target/release/yam-publish"
    dist = output or ROOT / "dist"
    dist.mkdir(parents=True, exist_ok=True)
    template = (ROOT / "packaging/yam/yam.yaml.in").read_text()
    for platform in selected:
        arch = targets()[platform]
        binary = ROOT / "dist" / f"station-airplay-linux-{arch}"
        if not binary.is_file():
            raise FileNotFoundError(f"{binary}: run sh scripts/build.sh {version} first")
        with tempfile.TemporaryDirectory(prefix="station-airplay-yam-") as tmp:
            stage = Path(tmp)
            (stage / "yam.yaml").write_text(template.replace("@VERSION@", version).replace("@PLATFORM@", platform))
            shutil.copy2(binary, stage / "station-airplay")
            (stage / "station-airplay").chmod(0o755)
            for name in ("launcher.sh", "service.sh", "postinstall.sh"):
                shutil.copy2(ROOT / "packaging/yam" / name, stage / name)
                (stage / name).chmod(0o755)
            for name in ("README.md", "LICENSE", "NOTICE", "AUTHORS"):
                shutil.copy2(ROOT / name, stage / name)
            (stage / "docs").mkdir()
            for name in ("yam.md", "releases.md", "SOURCE.md", "validation.md", "kuyase-validation.md", "kuyase-validation.json", "embedding.md"):
                shutil.copy2(ROOT / "docs" / name, stage / "docs" / name)
            shutil.copytree(ROOT / "docs/licenses", stage / "docs/licenses")
            subprocess.run([str(publisher), "pack", "--source", str(stage),
                            "--profiles", str(yam_source / "platforms"), "--platform", platform,
                            "--output", str(dist / f"station-airplay-{version}-{platform}.yampkg")], check=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True)
    parser.add_argument("--yam-source", type=Path, required=True)
    parser.add_argument("--platform", action="append", choices=targets())
    parser.add_argument("--output-dir", type=Path, help="package destination (default: dist/)")
    args = parser.parse_args()
    # Validate before substituting into YAML or constructing artifact paths.
    import importlib.util
    spec = importlib.util.spec_from_file_location("release_version", ROOT / "scripts/release-version.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    build(module.normalize(args.version), args.yam_source.resolve(), args.platform or targets(),
          args.output_dir.resolve() if args.output_dir else None)
