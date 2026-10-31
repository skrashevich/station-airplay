#!/usr/bin/env python3
"""Package corresponding source and pinned vendored dependencies for offline relinking."""
import argparse
import hashlib
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
VERSION = re.compile(r"[0-9A-Za-z._+-]+\Z")


def add_portable(archive, path, name):
    """Use Python's portable tar writer, without macOS extended-attribute sidecars."""
    def clean(info):
        if any(part.startswith("._") or part in (".DS_Store", "__pycache__") for part in Path(info.name).parts):
            return None
        info.uid = info.gid = 0
        info.uname = info.gname = ""
        info.pax_headers = {}
        return info
    archive.add(path, arcname=name, filter=clean)


def build(output=None, version="dev"):
    if not VERSION.fullmatch(version):
        raise ValueError("invalid version")
    output = Path(output or ROOT / f"dist/station-airplay-{version}-source.tar.gz").resolve()
    with tempfile.TemporaryDirectory(prefix="station-airplay-source-") as tmp:
        source = Path(tmp) / "station-airplay-source"
        source.mkdir()
        names = subprocess.check_output(
            ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT).split(b"\0")
        for raw in sorted(set(filter(None, names))):
            relative = Path(raw.decode())
            if relative.parts[0] in ("dist", "work", "vendor"):
                continue
            if any(part.startswith("._") or part in (".DS_Store", "__pycache__") for part in relative.parts):
                continue
            path = ROOT / relative
            # Deleted tracked paths and links to files outside this checkout are
            # deliberately not copied into the corresponding-source archive.
            if path.resolve() in (output, output.with_suffix(output.suffix + ".sha256")):
                continue
            if path.is_file() and not path.is_symlink():
                target = source / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(path.read_bytes())
                target.chmod(path.stat().st_mode & 0o777)
        # Includes the pinned AAC decoder and all other linked Go modules, their
        # copyright notices and licenses. No vendor directory changes the checkout.
        subprocess.run(["go", "mod", "vendor", "-o", str(source / "vendor")], cwd=ROOT, check=True)
        for required in ("go.mod", "go.sum", "LICENSE", "NOTICE", "docs/SOURCE.md",
                         "docs/licenses/go-aac-LGPL-3.0.txt", "docs/licenses/go-aac-GPL-3.0.txt",
                         "vendor/modules.txt", "vendor/github.com/skrashevich/go-aac",
                         "receiver.go", "cmd/station-airplay/main.go"):
            if not (source / required).exists():
                raise RuntimeError(f"missing corresponding source: {required}")
        (source / "REBUILD.md").write_text(f"""# Rebuilding Station AirPlay {version}

Use the Go toolchain version recorded in go.mod. The source archive contains
all linked Go dependency source in vendor/, including LGPL-3.0 go-aac.
You may modify that decoder or this library and relink a replacement binary.
With that Go toolchain installed, these commands require no module network access:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=vendor -trimpath -ldflags '-s -w -X main.version={version}' -o station-airplay-arm64 ./cmd/station-airplay
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -mod=vendor -trimpath -ldflags '-s -w -X main.version={version}' -o station-airplay-armv7 ./cmd/station-airplay
```

Go's own toolchain is available from https://go.dev/dl/ and is not bundled.
For AAC changes edit vendor/github.com/skrashevich/go-aac/ before building.
For a program embedding this receiver, modify the library source here or the
vendored copy in that program's corresponding-source package, then rebuild
that program using its included instructions.

A standalone rooted-speaker installation uses
/data/opt/station-airplay/bin/station-airplay. Stop the managed service before
replacement and restart it afterwards. You may also use scripts/install.sh
--binary /absolute/path/to/your/binary to install a replacement. Pairing state
is stored separately in the state directory. No signing key or authorization
token is required to install modified binaries on a rooted speaker.

License texts are in LICENSE, NOTICE, and docs/licenses/. Module-specific
licenses and notices are retained in vendor/. See docs/SOURCE.md for details.
""")
        output.parent.mkdir(parents=True, exist_ok=True)
        with tarfile.open(output, "w:gz", format=tarfile.PAX_FORMAT) as archive:
            add_portable(archive, source, source.name)
    output.with_suffix(output.suffix + ".sha256").write_text(
        f"{hashlib.sha256(output.read_bytes()).hexdigest()}  {output.name}\n")
    print(output)
    return output


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output")
    parser.add_argument("--version", default="dev")
    args = parser.parse_args()
    build(args.output, args.version)
