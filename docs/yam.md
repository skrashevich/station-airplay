# Установка через YAM

Нужны root-доступ, установленный [YAM v2](https://github.com/nyakokitsu/yam)
и прошивка с glibc/libasound, поддерживаемая station-airplay. Старый YAM с
пакетами `.yap` не подходит. Обновите менеджер: пакет использует executable-файлы
в `share/`, права которых старые версии YAM теряли. Формат и ограничения:
[упаковка YAM](https://github.com/nyakokitsu/yam/blob/main/docs/PACKAGING.md).

## Совместимые колонки

Поддерживаются все Яндекс Станции на Linux с glibc/ALSA и root-доступом;
Android-модели исключены. Выберите ID своего
профиля YAM; это ID менеджера, он не всегда совпадает с названием модели.

| Модель | Профиль | Приёмник |
| --- | --- | --- |
| Лайт | `yandexmicro` | ARM64 |
| Лайт 2 | `plum` (старый alias `yandexplum`) | ARM64 |
| Мини 1 / Мини 2 | `yandexmini` / `yandexmini_2` | ARM64 |
| Мини 3 / Мини 3 Про | `bergamot` / `pickle` | ARM64 |
| Миди | `cucumber` | ARM64 |
| Станция 2 | `yandexmidi` | ARM64 |
| Стрит | `mango` | ARMv7 |
| Станция 3 | `orion` | ARM64 |

Это список собираемых пакетов, а не подтверждение воспроизведения на каждой
модели. Требования к прошивке и результаты аппаратных проверок находятся в
[README](../README.md#требования-и-совместимость) и [validation.md](validation.md).
Макс (включая вариант с Zigbee, профиль `yandexstation_2`) и Станция первого
поколения на штатной прошивке (`yandexstation`) работают на Android и не
поддерживаются. См. [описание Макс](https://hackalice.me/devices/station-max/) и
[первого поколения](https://hackalice.me/devices/station-gen1/).
Профили хаба `saturn` и сторонней колонки `jbl_link_music` также в выпуск не входят.

## Установка по имени из реестра

Команды выполняются **на колонке**, в SSH/ADB shell. Добавьте официальный
реестр, если он ещё не настроен, и установите пакет:

```sh
yam registry add official https://repo.yse.wtf/v2 \
  --key ed25519:5Rz8F47uftWVU5kLgWYhprkEZ4rpP2kecUge/a7KQas=
yam install skrashevich/station-airplay
station-airplay -check
station-airplay -name 'Моя Станция AirPlay'
```

Для конкретной версии используйте `yam install skrashevich/station-airplay@0.1.1`.
Ключ и URL приведены из [документации YAM](https://github.com/nyakokitsu/yam#использование).

## Установка из GitHub Releases

Например, для Миди:

```sh
curl -fL https://github.com/skrashevich/station-airplay/releases/download/v0.1.1/install-yam.sh -o /tmp/install-station-airplay-yam.sh
sh /tmp/install-station-airplay-yam.sh cucumber v0.1.1
station-airplay -check
station-airplay -name 'Миди AirPlay'
```

При отсутствии `curl` скачайте скрипт через `wget -O`. Установщик загружает
`.yampkg` и `SHA256SUMS` того же релиза, проверяет SHA-256 и вызывает `yam install`.
Нужны `curl` или `wget`, `sha256sum`, HTTPS-доступ к GitHub. Хеш из того же
HTTPS-релиза проверяет целостность загрузки; подпись реестра в этом режиме не используется.
Проверка `-check` не воспроизводит звук; она проверяет интерфейс и библиотеки вывода.

Можно скачать пакет на ПК со [страницы релиза](https://github.com/skrashevich/station-airplay/releases/tag/v0.1.1),
сверить его SHA-256 с `SHA256SUMS`, передать по ADB/SSH и установить локально:

```sh
yam install --yes /tmp/station-airplay-0.1.1-cucumber.yampkg
yam doctor
yam service list
```

YAM проверяет профиль и архитектуру. Не переименовывайте пакет другого профиля:
например, `cucumber` и `yandexmidi` используют один ARM64 binary, но разные манифесты.

## Запуск и состояние

Команда `station-airplay` доступна через `$YAM_ROOT/bin`, обычно `/data/yam/bin`.
При необходимости используйте полный путь `/data/yam/bin/station-airplay`.
Она запускает приёмник на переднем плане и передаёт ему все аргументы CLI.
Состояние сопряжения и приватный кэш PCM находятся в
`$YAM_VAR/skrashevich.station-airplay`, по умолчанию
`$YAM_ROOT/var/skrashevich.station-airplay`. Корень определяется по установленному
пакету, поэтому нестандартный `YAM_ROOT` также поддерживается. Права новых
приватных каталогов ограничены владельцем.

Пакет объявляет службу `skrashevich/station-airplay/receiver` с политикой
`on-failure` и зависимостью от сети. **YAM пока только хранит декларации служб**:
сам по себе `yam install` не запускает приёмник и не включает автозапуск.
Для постоянной работы настройте существующий супервизор/root-окружение на
вызов `/data/yam/bin/station-airplay` после готовности сети. Останавливайте
процесс через SIGTERM и ждите его завершения перед обновлением/удалением.
При ручном запуске на переднем плане используйте Ctrl+C.

Перед первым запуском остановите старую автономную службу
`/data/opt/station-airplay/service stop` или встроенный AirPlay в Quasar Bridge,
если он включён: экземпляры используют одни порты. Чтобы сохранить прежнее
сопряжение автономной установки, после её остановки укажите тот же каталог:

```sh
station-airplay -data-dir /data/opt/station-airplay/state
```

Для обновления остановите приёмник, выполните скрипт с новым тегом и снова
запустите приёмник. Для удаления сначала остановите процесс, затем:

```sh
yam remove skrashevich/station-airplay
```

YAM удаляет пакет и ссылки; каталог состояния сохраняется. Полная очистка
состояния выполняется пользователем отдельно после удаления и отменяет сопряжение.

## Откуда берутся описание и список колонок

Текст задаётся полем `description` в `packaging/yam/yam.yaml.in`.
`scripts/build-yam.py` подставляет версию и профиль, а upstream `yam-publish pack`
записывает описание в `MANIFEST.yaml` внутри `.yampkg`. При публикации реестр
читает эти метаданные и показывает описание пакета в каталоге.

Профили и архитектуры перечислены в `packaging/yam/targets.tsv`. На каждый
профиль собирается отдельный архив. Веб-каталог реестра объединяет профили
артефактов **всех опубликованных версий**, а установщик проверяет наличие
артефакта именно выбранной версии. Поэтому удаление профиля из нового выпуска
само по себе не убирает его из списка на сайте: для этого нужно также убрать
ошибочный старый артефакт средствами реестра. Профили менеджера сами по себе
не подтверждают совместимость приложения.
Для исправления описания/списка уже опубликованной версии выпускается новая
версия пакета. Порядок подписанной публикации описан в [releases.md](releases.md).
