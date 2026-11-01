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
                         "vendor/github.com/ebitengine/purego", "docs/licenses/purego-Apache-2.0.txt",
                         "receiver.go", "cmd/station-airplay/main.go", "cmd/station-pcm/main.go",
                         "cmd/station-pcm-package/main.go", "internal/pcmbundle"):
            if not (source / required).exists():
                raise RuntimeError(f"missing corresponding source: {required}")
        (source / "REBUILD.md").write_text(f"""# Пересборка Station AirPlay {version}

Используйте версию инструментария Go, указанную в go.mod. Архив исходного кода содержит
исходный код всех подключаемых зависимостей Go в vendor/, включая go-aac под LGPL-3.0.
Вы можете изменить этот декодер или эту библиотеку и заново скомпоновать заменяющий бинарный файл.
При установленном инструментарии Go эти команды не требуют доступа к сети для загрузки модулей:

```sh
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -mod=vendor -trimpath -ldflags '-s -w -X main.version={version}' -o station-airplay-arm64 ./cmd/station-airplay
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -mod=vendor -trimpath -ldflags '-s -w -X main.version={version}' -o station-airplay-armv7 ./cmd/station-airplay
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOFLAGS=-mod=vendor go run github.com/skrashevich/station-airplay/cmd/station-pcm-package -arch arm64 -binary station-airplay-arm64
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOFLAGS=-mod=vendor go run github.com/skrashevich/station-airplay/cmd/station-pcm-package -arch armv7 -binary station-airplay-armv7
```

Сам инструментарий Go доступен на https://go.dev/dl/ и в архив не входит.
Для изменений AAC отредактируйте vendor/github.com/skrashevich/go-aac/ перед сборкой.
Для программы, встраивающей этот приёмник, измените исходный код библиотеки здесь или
копию в vendor пакета соответствующего исходного кода этой программы, затем пересоберите
программу по прилагаемым к ней инструкциям.

Автономная установка на колонку с root-доступом использует
/data/opt/station-airplay/bin/station-airplay. Перед заменой остановите управляемую службу,
а после замены снова запустите её. Для установки замены также можно использовать scripts/install.sh
--binary /absolute/path/to/your/binary. Состояние сопряжения
хранится отдельно в каталоге состояния. Для установки изменённых бинарных файлов на колонку
с root-доступом не требуются ключ подписи или токен авторизации.

Общая команда упаковки PCM дописывает сжатые модули нативного проигрывателя к
ELF-файлу приёмника, создавая один устанавливаемый исполняемый файл. Исполняемые файлы ARM64 содержат
модули ARMv7 и ARM64; исполняемые файлы ARMv7 содержат только ARMv7. При проверке или включении
AirPlay в приватный кэш состояния приёмника извлекается только подходящий модуль.
Он динамически использует glibc/ALSA прошивки, тогда как основной
приёмник остаётся статическим. Устанавливать лаунчер или сопутствующий исполняемый файл не нужно.
Передайте -output /path/to/copy, чтобы сохранить входной бинарный файл; без него упаковка
выполняется на месте атомарно. Необязательные -armv7-binary и -arm64-binary переиспользуют
модули, собранные разработчиком. Если приёмник сжимается UPX, сделайте это до этого шага упаковки.
Полного упакованного бинарного файла достаточно для scripts/install.sh --binary.

Тексты лицензий находятся в LICENSE, NOTICE и docs/licenses/. Лицензии и уведомления
конкретных модулей сохранены в vendor/. Подробности см. в docs/SOURCE.md.
""", encoding="utf-8")
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
