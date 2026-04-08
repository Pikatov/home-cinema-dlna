# Home Cinema — DLNA/UPnP медиа-сервер для домашнего кинотеатра

[English version →](README.en.md)

Home Cinema — лёгкий DLNA/UPnP медиасервер на Go для домашней сети. Он поднимает `MediaServer` и `ContentDirectory`, раздаёт видео по HTTP с `Range`, сохраняет прогресс просмотра и умеет отдавать альтернативный TV-поток через `ffmpeg`, чтобы уменьшить фризы на слабом Wi‑Fi.

В репозиторий также входит macOS-приложение Home Cinema на SwiftUI: можно выбрать медиатеку, запустить или остановить сервер, открыть логи и переключить светлую или тёмную тему с glass UI.

MacOS App Screenshots:

<img width="408" height="276" alt="Light" src="https://github.com/user-attachments/assets/be67e61d-b829-4381-9376-9aa1db8acaf9" /><img width="408" height="276" alt="Dark" src="https://github.com/user-attachments/assets/08360764-2e21-460d-94dd-aacd629a8afc" />

## Когда это полезно
- ТВ или приставка видит DLNA, но SMB-шара воспроизводится нестабильно.
- Нужен сервер без Plex/Jellyfin и без отдельной базы данных.
- Важно, чтобы перемотка и “продолжить просмотр” переживали рестарты клиента.

## Возможности
- DLNA/UPnP: SSDP, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming с `Range`.
- Видео: `.mp4`, `.mkv`, `.avi`, `.m4v`.
- Прогресс просмотра с сохранением на диск и восстановлением после перезапуска клиента.
- Альтернативный TV-поток через `ffmpeg` для более плавного воспроизведения по Wi‑Fi.
- Кеширование Browse и безопасная смена папки через `POST /set-media-dir`.
- macOS app со светлой и тёмной темой.

## Быстрый старт
Требование: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

На ТВ или приставке выберите сервер **Home Cinema** в списке DLNA-источников.

## Если видео фризит по Wi‑Fi
Опция `--tv-stream` включена по умолчанию и добавляет альтернативный поток через `ffmpeg`.

- Если `ffmpeg` не установлен, сервер автоматически отключит TV-поток и напишет предупреждение в лог.
- Для длительности и таймкодов нужен `ffprobe`.

Пример настройки:

```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Логи и прогресс
По умолчанию сервер хранит данные в:

- macOS: `~/Library/Application Support/HomeCinema/`

Там лежат:

- `server.log`
- `progress.json`

Папку можно переопределить через `HOMECINEMA_DATA_DIR` или `--data-dir`.

## Управление и безопасность
`/set-media-dir` по умолчанию принимает только `POST` и доступен только с `localhost`.

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
Сборка `.app`:

```bash
./build/home-cinema/build_app.sh
```

Запуск приложения:

```bash
./build/home-cinema/run_control_app.sh
```

Приложение собирает универсальный `.app`, вкладывает внутрь свежий бинарь сервера и использует helper-скрипты для запуска и остановки сервера. При запуске через приложение сервер также хранит логи и прогресс в `~/Library/Application Support/HomeCinema/`.

## Локальная проверка перед git
В репозитории есть скрипт pre-publish проверки:

```bash
./scripts/prepublish_check.sh
```

Он делает три вещи:

- ищет очевидные sensitive-паттерны
- гоняет `go test ./...`
- показывает untracked файлы и текущий `git status`

## Тесты
В репозитории есть набор unit-тестов для ключевой логики сервера:

- XML-экранирование
- безопасная нормализация и склейка путей
- парсинг `Range`
- сохранение и загрузка прогресса просмотра

Запуск вручную:

```bash
go test ./...
```

## Полезные флаги
- `--media-dir` — папка медиатеки.
- `--port` — HTTP-порт сервера.
- `--tv-stream` — включить или выключить TV-поток.
- `--tv-stream-first` — поставить TV-поток первым в DLNA-ресурсах.
- `--stream-buf-mb` — размер буфера стриминга.
- `--warmup-meta` и `--warmup-meta-throttle` — прогрев метаданных через `ffprobe`.

Полный список:

```bash
./HomeCinemaServer -h
```

## Структура проекта
- `app.go`, `browse.go`, `streaming.go`, `progress.go`, `metadata.go` и соседние файлы — серверная логика.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI macOS app Home Cinema.
- `build/home-cinema/build_app.sh` — сборка `.app`.
- `build/home-cinema/run_control_app.sh` — запуск `.app`.
- `scripts/prepublish_check.sh` — локальная проверка перед публикацией.

## Лицензия и изменения
- MIT — [LICENSE](LICENSE)
- История изменений — [CHANGELOG.md](CHANGELOG.md)
