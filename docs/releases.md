# Сборка и публикация релизов

Версия пакета хранится в `VERSION` и должна иметь вид `MAJOR.MINOR.PATCH`.
Git-теги `v0.1` и `v0.1.0` соответствуют версии YAM `0.1.0`; для одного
выпуска создавайте только один тег. `scripts/release-version.py` нормализует тег,
workflow проверяет совпадение с `VERSION`. Первый релиз использует тег `v0.1`.

## Локальная сборка

Нужны Go версии из `go.mod` или новее, Python 3.9+, Rust/Cargo и системный
компилятор C для host-инструмента YAM (SQLite/zstd). Приёмник и его PCM-модули
по-прежнему собираются с `CGO_ENABLED=0`.

```sh
sh scripts/yam_test.sh
go test -race ./...
sh scripts/build.sh "$(cat VERSION)"
git clone https://github.com/nyakokitsu/yam.git .yam-tools
git -C .yam-tools checkout c753064bb2320305df61c575440bf739e47551b8
cargo build --release --locked --manifest-path .yam-tools/Cargo.toml --bin yam-publish --bin yam
python3 scripts/build-yam.py --version "$(cat VERSION)" --yam-source .yam-tools
python3 scripts/check-yam.py --version "$(cat VERSION)" --yam-source .yam-tools
cp scripts/install-yam.sh dist/install-yam.sh
python3 scripts/checksums.py "$(cat VERSION)"
(cd dist && sha256sum -c SHA256SUMS)
```

На macOS вместо последней команды можно использовать `shasum -a 256 -c SHA256SUMS`.
Начинайте сборку выпуска с пустым `dist/`. В контрольные суммы попадают только
артефакты выбранной версии и установщик. `scripts/build.sh` запускает тесты установщика, Go tests и vet,
собирает два исполняемых файла со встроенными PCM-модулями, автономные архивы
и соответствующий исходный код с vendor. `build-yam.py` использует именно эти
исполняемые файлы; свой формат `.yampkg` проект не реализует.

В `packaging/yam/targets.tsv` перечислены профили и архитектуры. Можно собрать
один пакет через `--platform cucumber`. Шаблон `yam.yaml.in` содержит имя,
лицензии, файлы и декларацию службы. `check-yam.py` на настоящем host CLI YAM
проверяет установку, права executable, декларацию службы, переустановку,
сохранность состояния, удаление и отказ для неправильной платформы.
Эти проверки не запускают ARM binary и не заменяют проверку звука на колонке.

## GitHub Actions

- `ci.yml`: push в `main` и pull request; вызывает общий `build.yml`.
- `build.yml`: race tests, сборка, vendored source, все `.yampkg`, lifecycle YAM,
  SHA-256; сохраняет полный набор как artifact `release-assets`.
- `release.yml`: push тега `v*`; проверяет версию и вызывает ту же сборку,
  создаёт draft, загружает весь набор и публикует GitHub Release.
- `yam-publish.yml`: отдельный ручной запуск для подписанной отправки уже
  выпущенных `.yampkg` в реестр, без пересборки артефактов.

Для GitHub Release достаточно встроенного `GITHUB_TOKEN`; `contents: write`
запрашивает только job публикации. Нужны включённые Actions в репозитории.
Релиз содержит `.yampkg` каждого профиля, два автономных `.tar.gz`,
`station-airplay-VERSION-source.tar.gz`, `install-yam.sh` и `SHA256SUMS`.
Raw binary без соответствующих уведомлений/исходников отдельно не публикуется.

Порядок выпуска: обновить `VERSION` и заметки `docs/releases/VERSION.md`,
проверить CI, закоммитить и отправить изменения, затем создать и отправить
аннотированный тег. Для первого выпуска:

```sh
git tag -a v0.1 -m 'Station AirPlay v0.1'
git push origin v0.1
```

Если заметок для версии нет, workflow использует GitHub generated release notes.
При сбое загрузки остаётся draft: проверьте job, удалите неполный draft через
GitHub UI/CLI и перезапустите workflow. Опубликованные артефакты не заменяйте:
выпускайте новую версию. Workflow реестра отправляет профили последовательно;
при частичной публикации не перезапускайте её вслепую, так как комбинация
имени/версии/профиля в реестре неизменяема.

## Подписанная публикация в YAM Registry

Сначала займите namespace `skrashevich` на [repo.yse.wtf](https://repo.yse.wtf/)
и создайте ключ/токен согласно [инструкции YAM](https://github.com/nyakokitsu/yam/blob/main/docs/PUBLISHING.md).
Добавьте в GitHub repository secrets:

- `YAM_PUBLISH_TOKEN` — publish-токен этого namespace;
- `YAM_AUTHOR_PRIVATE_KEY` — полное содержимое Ed25519 private key PEM PKCS#8.

Запустите **Publish to YAM registry**, указав существующий тег релиза.
Workflow проверяет SHA-256 скачанных артефактов и использует upstream
`yam-publish publish`. PEM создаётся с правами `0600` и удаляется при выходе;
секреты передаются через окружение. До настройки credentials и успешной
публикации пользователи устанавливают `.yampkg` из GitHub Releases.

Ключи и publish-токены на колонке не требуются. Для замены изменённого
исполняемого файла можно пересобрать исходники и свой `.yampkg`, а затем
установить его локально. Соответствующий исходный код и лицензии описаны в
[SOURCE.md](SOURCE.md).
