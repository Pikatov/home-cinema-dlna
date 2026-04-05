# Home Cinema — DLNA/UPnP медиа‑сервер для домашнего кинотеатра

[English version →](README.en.md)

Небольшой DLNA/UPnP медиасервер на Go. Поднимает UPnP `MediaServer` + `ContentDirectory`, раздаёт видео по HTTP с поддержкой `Range` (перемотка/продолжение просмотра), запоминает прогресс и может отдавать “TV‑поток” через `ffmpeg`, чтобы убрать фризы на слабом Wi‑Fi.

В комплекте есть приложение управления для macOS (SwiftUI): выбрать папку, запустить/остановить сервер, открыть логи.

## Когда это полезно
- ТВ/приставка видит DLNA, но воспроизведение по SMB/сетевым шарам “подвисает”.
- Хотите обычную папку с фильмами, без Plex/Jellyfin и без базы данных.
- Нужно, чтобы перемотка и “продолжить просмотр” работали стабильно.

## Возможности
- DLNA/UPnP: SSDP‑анонс, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming с `Range`.
- Видео: `.mp4`, `.mkv`, `.avi`.
- Кеширование результатов Browse (ускоряет навигацию на некоторых ТВ).
- Прогресс просмотра: сохраняет позицию и показывает таймкод в имени файла (например `Movie.mkv [▶ 12:34]`).
- Быстрая смена медиатеки: `POST /set-media-dir` (по умолчанию только с `localhost`).

## Быстрый старт (сервер)
Требование: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

Дальше на ТВ/приставке откройте список DLNA/медиасерверов и выберите **Home Cinema**.

## Если видео “фризит” по Wi‑Fi
Опция `--tv-stream` (включена по умолчанию) добавляет альтернативный поток через `ffmpeg`, который ограничивает пики битрейта.

- Если `ffmpeg` не установлен, сервер автоматически отключит TV‑поток и напишет предупреждение в лог.
- Для таймкодов/длительности нужен `ffprobe` (обычно идёт вместе с `ffmpeg`).

Пример настройки:
```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Где хранятся логи и прогресс
По умолчанию:
- macOS: `~/Library/Application Support/HomeCinema/` (`server.log`, `progress.json`)

Переопределение:
- переменная окружения `HOMECINEMA_DATA_DIR`
- или `--data-dir "/путь/к/папке"`

## Управление и безопасность
Эндпоинт смены папки (`/set-media-dir`) по умолчанию доступен **только с localhost** — чтобы устройство в локальной сети не могло удалённо переключить медиатеку на произвольный каталог.

Если осознанно хотите разрешить удалённое управление:
```bash
./HomeCinemaServer --allow-remote-control
```

Полезные запросы:
```bash
# статус (для localhost — полный путь, из сети — только имя папки)
curl -s http://127.0.0.1:8080/ | jq

# сменить папку медиатеки (по умолчанию только localhost)
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir | jq
```

## Приложение управления (macOS, SwiftUI)
<img width="1012" height="564" alt="Home Cinema Control (macOS)" src="https://github.com/user-attachments/assets/1b69e1b5-4241-4d5f-bfe6-9b299cf58dcd" />

Сборка:
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```

Скрипт собирает универсальный `.app` и вкладывает внутрь свежий бинарь сервера. Логи UI/запуска пишутся в `/tmp/homecinema.log`.

## Сборка DMG (опционально)
Скрипт `build/home-cinema/make_dmg.sh` упаковывает `.app` в DMG.

Зависимость:
```bash
brew install create-dmg
```

Сборка:
```bash
./build/home-cinema/make_dmg.sh
```

## Полезные флаги
- `--media-dir` — папка медиатеки.
- `--port` — HTTP‑порт сервера (по умолчанию `8080`).
- `--tv-stream` — включить/выключить TV‑поток (нужен `ffmpeg`).
- `--tv-stream-first` — поставить TV‑поток первым (ТВ чаще выбирает первый ресурс, но может пропасть прогресс/длительность).
- `--stream-buf-mb` — буфер выдачи; если сеть “подвисает”, попробуйте `1–2`.
- `--warmup-meta` / `--warmup-meta-throttle` — прогрев метаданных через `ffprobe` (может грузить диск/CPU).

Полный список: `./HomeCinemaServer -h`.

## Структура проекта
- `main.go` — DLNA/UPnP сервер.
- `build/home-cinema/HomeCinemaControlSwift/` — приложение управления (SwiftUI).
- `build/home-cinema/make_dmg.sh` — упаковка DMG.

## Лицензия и изменения
- Лицензия: MIT (`LICENSE`)
- История изменений: `CHANGELOG.md`
