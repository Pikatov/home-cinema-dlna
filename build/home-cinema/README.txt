Home Cinema (macOS)
===================

This folder contains build artifacts and helper scripts for the macOS control app.

Recommended: SwiftUI control app
--------------------------------

Build:
  cd build/home-cinema/HomeCinemaControlSwift
  ./build.sh

The script produces “Home Cinema Control.app” and bundles a fresh server binary inside.
UI/start logs are written to /tmp/homecinema.log.

Server binary
-------------

If you want to run the server directly from this folder:
  cd build/home-cinema
  ./HomeCinemaServer --media-dir "/path/to/movies"

Legacy (AppleScript) control dialog
-----------------------------------

HomeCinemaControl.applescript is a small legacy control dialog.

Build the .app wrapper:
  /usr/bin/osacompile -o build/home-cinema/HomeCinemaControl.app build/home-cinema/HomeCinemaControl.applescript

Notes:
- The script launches the server in a detached `screen` session named “homecinema”.
- Folder changes are sent to http://127.0.0.1:8080/set-media-dir (localhost-only by default).
