#!/bin/bash
set -euo pipefail

# --- 1. КОНФИГУРАЦИЯ ПУТЕЙ ---
# Скрипт определяет корень проекта относительно своего местоположения
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# Путь к вашему скомпилированному приложению
APP_DIR="$ROOT_DIR/home-cinema/HomeCinemaControlSwift/Home Cinema Control.app"
# Название тома при монтировании
VOL_NAME="Home Cinema Control"
# Куда сохранить готовый файл
FINAL_DMG="$ROOT_DIR/home-cinema/HomeCinemaControl.dmg"

# Путь к вашей НОВОЙ картинке (сохраните её под этим именем рядом со скриптом)
BG_SRC="$ROOT_DIR/home-cinema/dmg-background.png"
# Иконка диска (берем из ресурсов самого приложения)
ICON_SRC="$APP_DIR/Contents/Resources/HomeCinema.icns"

# Временные папки для сборки
STAGING_DIR="$ROOT_DIR/home-cinema/dmg-staging"
WINDOW_SIZE=540

# --- 2. ПРОВЕРКИ ---
if [ ! -d "$APP_DIR" ]; then
  echo "Ошибка: Приложение не найдено по пути: $APP_DIR" >&2
  exit 1
fi

if ! command -v create-dmg >/dev/null 2>&1; then
  echo "Ошибка: create-dmg не установлен. Выполните: brew install create-dmg" >&2
  exit 1
fi

# --- 3. ПОДГОТОВКА ---
echo "Очистка временных файлов..."
rm -rf "$STAGING_DIR" "$FINAL_DMG"
mkdir -p "$STAGING_DIR"

# Копируем .app во временную директорию (оттуда create-dmg заберет файлы)
cp -R "$APP_DIR" "$STAGING_DIR/"

# --- 4. СБОРКА DMG ---
echo "Запуск сборки с новым фоном..."

# Параметры размещения иконок:
# 140 270 — иконка приложения (слева по центру)
# 400 270 — иконка Applications (справа по центру)

create-dmg \
  --volname "$VOL_NAME" \
  --volicon "$ICON_SRC" \
  --background "$BG_SRC" \
  --window-pos 200 120 \
  --window-size $WINDOW_SIZE $WINDOW_SIZE \
  --icon-size 120 \
  --icon "Home Cinema Control.app" 140 270 \
  --hide-extension "Home Cinema Control.app" \
  --app-drop-link 400 270 \
  "$FINAL_DMG" \
  "$STAGING_DIR"

# --- 5. ЗАВЕРШЕНИЕ ---
rm -rf "$STAGING_DIR"

echo "------------------------------------------------"
echo "Успех! Современный DMG создан: $FINAL_DMG"
echo "------------------------------------------------"