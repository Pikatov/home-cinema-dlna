# Home Cinema (DLNA/UPnP Media Server)

Минимальный DLNA/UPnP медиасервер на Go для macOS (и других ОС, где доступен Go). Поднимает UPnP MediaServer + ContentDirectory и раздаёт видеофайлы по HTTP с поддержкой `Range` (перемотка/продолжение просмотра).

В репозитории также есть простое приложение управления для macOS (SwiftUI), которое умеет выбрать папку, запускать/останавливать сервер и открывать логи.

## Возможности
- UPnP/DLNA: SSDP-анонс, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming с `Range`.
- Поддержка видео: `.mp4`, `.mkv`, `.avi`.
- Кеширование результата Browse (ускоряет навигацию на некоторых ТВ).
- Прогресс просмотра: сервер сохраняет позицию и показывает таймкод в имени файла (например: `Movie.mkv [▶ 12:34]`).
- Смена медиатеки локально: `POST /set-media-dir` (по умолчанию доступно только с `localhost`).

## Требования
- Go 1.26+.
- (Опционально) `ffprobe` из FFmpeg — нужно для вычисления длительности (таймкод в названии, `duration` в DLNA). Без `ffprobe` сервер продолжит работать, но таймкоды/длительность могут не отображаться.
- (Опционально) `ffmpeg` — нужен для TV-потока (`/tv/`, флаг `--tv-stream`), который ограничивает пики битрейта и помогает убрать зависания на слабом Wi‑Fi. Без `ffmpeg` отключите `--tv-stream=false`.
- Для сборки UI: macOS 12+ и Xcode Command Line Tools (`xcrun`, `swiftc`, `sips`, `iconutil`).

## Быстрый старт (сервер из исходников)
```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

После запуска откройте на ТВ/приставке список DLNA/медиасерверов и выберите **Home Cinema**.

Если воспроизведение тормозит по Wi‑Fi, включите TV-поток (включён по умолчанию) и подберите `--tv-maxrate-mbps`:
```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Где хранятся логи и прогресс
По умолчанию сервер пишет состояние в пользовательскую папку:
- macOS: `~/Library/Application Support/HomeCinema/` (файлы `server.log` и `progress.json`)

Можно переопределить:
- переменной окружения `HOMECINEMA_DATA_DIR`
- или флагом `--data-dir "/путь/к/папке"`

## Локальное управление и безопасность
Эндпоинт смены папки (`/set-media-dir`) по умолчанию доступен **только с localhost**. Это сделано, чтобы устройства в локальной сети не могли удалённо переключить сервер на произвольные каталоги.

Если вы понимаете риски и хотите разрешить удалённое управление, запустите сервер с флагом:
```bash
./HomeCinemaServer --allow-remote-control
```

Полезные запросы:
```bash
# статус (для localhost возвращает полный путь, для сети — только имя папки)
curl -s http://127.0.0.1:8080/ | jq

# сменить папку медиатеки (по умолчанию только localhost)
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir | jq
```

## SwiftUI-приложение управления (macOS)

<img width="1012" height="564" alt="Снимок экрана 2026-03-29 в 10 45 05" src="https://github.com/user-attachments/assets/1b69e1b5-4241-4d5f-bfe6-9b299cf58dcd" />


Сборка:
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```

Скрипт соберёт универсальный `.app` и вложит внутрь свежий бинарь сервера. Логи UI/запуска пишутся в `/tmp/homecinema.log` (кнопка **Logs** открывает этот файл).

## Конфигурация и приватность
- Путь к медиатеке передаётся флагом `--media-dir` или через UI; не коммитится.
- Логи и локальные пути добавлены в `.gitignore`.

## Сборка DMG (опционально)
Скрипт `build/home-cinema/make_dmg.sh` собирает красивый DMG (артефакт **не** коммитится).

Зависимость:
```bash
brew install create-dmg
```

Сборка:
```bash
./build/home-cinema/make_dmg.sh
```

## Структура проекта
- `main.go` — DLNA/UPnP сервер.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI UI + `build.sh`.
- `build/home-cinema/make_dmg.sh`, `build/home-cinema/dmg-background.png` — упаковка DMG.

## Лицензия
MIT — см. `LICENSE`.

## Changelog
См. `CHANGELOG.md`.

---

# Home Cinema (DLNA/UPnP Media Server) — English

A minimal DLNA/UPnP media server in Go for macOS (and other OSes where Go is available). It brings up a UPnP MediaServer + ContentDirectory and serves video files over HTTP with `Range` support (seeking/resuming playback).

This repository also includes a simple macOS control app (SwiftUI) that can pick a folder, start/stop the server, and open logs.

## Features
- UPnP/DLNA: SSDP announcements, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming with `Range`.
- Video support: `.mp4`, `.mkv`, `.avi`.
- Browse result caching (speeds up navigation on some TVs).
- Watch progress: the server stores the position and shows a timecode in the file name (e.g.: `Movie.mkv [▶ 12:34]`).
- Switch the media library locally: `POST /set-media-dir` (by default available only from `localhost`).

## Requirements
- Go 1.26+.
- (Optional) `ffprobe` from FFmpeg — needed to calculate duration (timecode in the file name, `duration` in DLNA). Without `ffprobe` the server will still work, but timecodes/duration may not be shown.
- (Optional) `ffmpeg` — needed for the TV stream (`/tv/`, `--tv-stream` flag), which limits bitrate peaks and helps prevent stuttering on weak Wi‑Fi. Without `ffmpeg`, disable it with `--tv-stream=false`.
- To build the UI: macOS 12+ and Xcode Command Line Tools (`xcrun`, `swiftc`, `sips`, `iconutil`).

## Quick start (server from sources)
```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

After starting, open the list of DLNA/media servers on your TV/set-top box and choose **Home Cinema**.

If playback stutters over Wi‑Fi, enable the TV stream (enabled by default) and tune `--tv-maxrate-mbps`:
```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Where logs and progress are stored
By default, the server writes its state into the user folder:
- macOS: `~/Library/Application Support/HomeCinema/` (files `server.log` and `progress.json`)

You can override it:
- with the `HOMECINEMA_DATA_DIR` environment variable
- or with the `--data-dir "/path/to/folder"` flag

## Local control and security
The endpoint for changing the folder (`/set-media-dir`) is available **only from localhost** by default. This is done so that devices on the local network cannot remotely switch the server to arbitrary directories.

If you understand the risks and want to allow remote control, start the server with:
```bash
./HomeCinemaServer --allow-remote-control
```

Useful requests:
```bash
# status (for localhost returns the full path, for the network — only the folder name)
curl -s http://127.0.0.1:8080/ | jq

# change media library folder (by default localhost only)
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir | jq
```

## SwiftUI control app (macOS)
Build:
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```

The script will build a universal `.app` and bundle a fresh server binary inside. UI/start logs are written to `/tmp/homecinema.log` (the **Logs** button opens this file).

## Configuration and privacy
- The media library path is passed via the `--media-dir` flag or via the UI; it is not committed.
- Logs and local paths are added to `.gitignore`.

## Build DMG (optional)
The `build/home-cinema/make_dmg.sh` script builds a nice DMG (the artifact is **not** committed).

Dependency:
```bash
brew install create-dmg
```

Build:
```bash
./build/home-cinema/make_dmg.sh
```

## Project structure
- `main.go` — DLNA/UPnP server.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI UI + `build.sh`.
- `build/home-cinema/make_dmg.sh`, `build/home-cinema/dmg-background.png` — DMG packaging.

## License
MIT — see `LICENSE`.

## Changelog
See `CHANGELOG.md`.
