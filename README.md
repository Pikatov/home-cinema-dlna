# Home Cinema

[English version →](README.en.md)

Home Cinema — небольшой DLNA/UPnP-сервер на Go и аккуратное macOS-приложение для управления им. Если хочется просто открыть папку с фильмами на телевизоре без Plex, Jellyfin, базы данных и тяжёлой настройки, проект именно про это.

Актуальная версия, подготовленная в этом репозитории: **1.8**.

macOS app screenshots:

<img width="408" height="276" alt="light" src="https://github.com/user-attachments/assets/485c6f9a-8360-438d-b077-8fd34a576a07" />
<img width="408" height="276" alt="night" src="https://github.com/user-attachments/assets/600089e3-88f8-44aa-90e6-f0b72ccda2f0" />


## Что умеет проект
- Поднимает DLNA/UPnP `MediaServer` и `ContentDirectory`.
- Отдаёт видео по HTTP с `Range`, чтобы перемотка и продолжение просмотра работали предсказуемо.
- Сохраняет прогресс просмотра на диск и переживает перезапуск клиента или сервера.
- Может продолжить просмотр с сохранённого места для TV-потока и `/resume/`-сценария.
- Умеет показывать и сбрасывать сохранённый прогресс через macOS-приложение, включая удаление отдельных записей.
- Может отдавать альтернативный TV-поток через `ffmpeg`, если обычное воспроизведение по Wi‑Fi нестабильно.
- Даёт локальные control-endpoints для смены папки, сброса прогресса и удаления отдельной записи прогресса.

## Когда это особенно полезно
- ТВ или приставка видит DLNA, но SMB или сетевые шары воспроизводятся с фризами.
- Нужен простой домашний сервер без отдельной медиатеки, постеров и лишнего слоя инфраструктуры.
- Важно, чтобы "продолжить просмотр" не терялось после перезапуска устройства.

## Быстрый старт
Требование: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

После запуска выберите **Home Cinema** в списке DLNA-источников на телевизоре или приставке.

## Если видео фризит по Wi‑Fi
По умолчанию включён `--tv-stream`: сервер может добавить альтернативный поток через `ffmpeg`. Для тяжёлых файлов и некоторых HEVC/HDR MKV этот TV-поток автоматически ставится первым, потому что многие телевизоры не запускают такие MKV напрямую.

- Если `ffmpeg` не установлен, сервер просто отключит TV-поток и напишет это в лог.
- Для длительности и таймкодов полезен `ffprobe`.
- Часть ТВ показывает такой совместимый поток как `mpg` или рисует конец фильма как `0:00`. Сервер отдаёт DLNA-заголовки длительности и time-seek, но отображение зависит от конкретного ТВ.

Пример настройки:

```bash
./HomeCinemaServer \
  --tv-stream=true \
  --tv-auto-first=true \
  --tv-auto-first-mbps 18 \
  --tv-maxrate-mbps 8 \
  --tv-bufsize-mbps 16 \
  --tv-crf 23
```

## Где лежат логи и прогресс
По умолчанию данные хранятся в:

- macOS: `~/Library/Application Support/HomeCinema/`

Основные файлы:

- `server.log`
- `progress.json`

Папку можно переопределить через `HOMECINEMA_DATA_DIR` или `--data-dir`.

Если фильм уже удалён из медиатеки, сервер не держит такую запись в списке прогресса: устаревшие элементы вычищаются при смене папки и периодически раз в час.

Сохранённый прогресс используется в названии фильма (`[▶ 12:34 - 1:45:00]`), в macOS-приложении и в TV-потоке, когда сервер стартует ffmpeg с уже известной позиции. Для некоторых DLNA-клиентов перемотка и отображение полного времени всё равно могут отличаться от обычного локального плеера.

## Управление и безопасность
Control-endpoints по умолчанию доступны только локально:

- `POST /set-media-dir`
- `POST /reset-progress`
- `POST /delete-progress`

Если осознанно хотите разрешить удалённое управление:

```bash
./HomeCinemaServer --allow-remote-control
```

Полезные запросы:

```bash
# статус
curl -s http://127.0.0.1:8080/

# смена папки медиатеки
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir
```

## macOS-приложение
Собрать `.app`:

```bash
./build/home-cinema/build_app.sh
```

Запустить:

```bash
./build/home-cinema/run_control_app.sh
```

Приложение собирает универсальный `Home Cinema.app`, вкладывает внутрь свежий бинарь сервера и использует helper-скрипты для запуска и остановки. В версии `1.8` окно показывает `ON AIR`, активные стримы, сохранённый прогресс и даёт undo после сброса прогресса. Polling статуса pause-ится, когда окно неактивно.

Требование: **macOS 14 (Sonoma)** или новее. На более ранних macOS подойдёт standalone server-бинарь.

## Локальная проверка перед релизом
В репозитории есть небольшой pre-publish скрипт:

```bash
./scripts/prepublish_check.sh
```

Он:

- ищет очевидные sensitive-паттерны
- запускает `go test ./...`
- показывает untracked-файлы и текущий `git status`

## Тесты
Покрыты ключевые части серверной логики:

- XML-экранирование
- безопасная нормализация и склейка путей
- парсинг `Range`
- DLNA duration/time-seek заголовки
- активные сессии `/stats`
- сохранение и загрузка прогресса просмотра

Ручной запуск:

```bash
go test ./...
```

## Структура проекта
- `app.go`, `browse.go`, `streaming.go`, `progress.go`, `metadata.go` и соседние файлы — серверная часть.
- `build/home-cinema/HomeCinemaControlSwift/` — сборка macOS-приложения и его ресурсы.
- `build/home-cinema/HomeCinemaControlSwift/Sources/HomeCinemaControlSwift/` — актуальные SwiftUI-исходники интерфейса.
- `build/home-cinema/build_app.sh` — сборка `.app`.
- `build/home-cinema/run_control_app.sh` — запуск приложения.
- `scripts/prepublish_check.sh` — локальная проверка перед публикацией.

## Документы
- История изменений — [CHANGELOG.md](CHANGELOG.md)
- Release notes для `1.8` — [releases/v1.8.md](releases/v1.8.md)
- English release notes for `1.8` — [releases/v1.8.en.md](releases/v1.8.en.md)
- Release notes для `1.7` — [releases/v1.7.md](releases/v1.7.md)
- English release notes for `1.7` — [releases/v1.7.en.md](releases/v1.7.en.md)
- Политика безопасности — [SECURITY.md](SECURITY.md)
- Лицензия — [LICENSE](LICENSE)
