# Corresponding source and rebuilding

Each standalone release includes `station-airplay-VERSION-source.tar.gz` beside
the Linux ARM64 and ARMv7 binary archives. Download the source archive for the
same version as your binary. `SHA256SUMS` identifies the matching archives.
The source archive contains this receiver library, the standalone CLI, build
and installation scripts, `go.mod`, `go.sum`, and the exact linked dependency
sources under `vendor/`. Its `REBUILD.md` provides offline `CGO_ENABLED=0` build
commands for both speaker architectures with an installed Go toolchain.

The AAC decoder is [go-aac](https://github.com/skrashevich/go-aac), distributed
under LGPL-3.0. The source archive includes the decoder source, its notices,
and both the LGPL-3.0 and GPL-3.0 license texts in `docs/licenses/`. You can
modify its vendored source and relink this application; the build does not
require a signing key. See the source archive's `REBUILD.md` for replacement
instructions. The receiver's Apache-2.0 license and attribution are in
`LICENSE` and `NOTICE`; dependency-specific notices remain in `vendor/`.

Developers create binary and source archives with `sh scripts/build.sh VERSION`.
To create only the source archive from a checkout, run
`python3 scripts/build-source-package.py --version VERSION`. Source packaging
includes current tracked and untracked nonignored files, so release builds
must run from the reviewed release checkout. Generated `dist/`, `work/`, and
`vendor/` directories and macOS metadata files are excluded. The packaging
step vendors dependencies into a temporary staging directory without changing
the working checkout; dependency downloads may be needed during packaging.
The resulting source archive builds without downloading Go modules.
