# Home Cinema v1.7 (2026-04-14)

## Highlights
- **macOS app:** the control window now feels like a more cohesive glass-style control deck. It adds an `Auto/Light/Dark` theme switcher, gentle animated background motion, more tactile hover/press states, and a tighter overall layout.
- **Progress UX:** the saved-progress section is now cleaner and more compact, with a slim progress bar, clearer labels, and calmer inline removal for individual entries.
- **Resume accuracy:** when a movie starts from a saved position, the server now keeps saving progress from the actual seek offset instead of restarting that calculation from zero.
- **Repo cleanup:** obsolete SwiftUI placeholder files were removed, and the real SwiftPM sources under `Sources/HomeCinemaControlSwift/*` are no longer hidden by an overly broad `.gitignore` rule.

## App changes
- The top area is more focused: one hero panel, less visual noise, and a clearer online/offline state.
- Primary actions now use action-card styling with hover/press feedback and a built-in spinner while the app is busy.
- Saved-progress rows show better context with file name, folder, timecode, last update text, and a compact progress indicator.

## Progress-saving fix
- Resuming playback from an existing saved position no longer loses the initial seek offset on the next progress write.
- This makes long-movie resume behavior much more consistent after restart or reconnect scenarios.

## Repository and release prep
- `README.md` and `README.en.md` were updated for the `1.7` release.
- `CHANGELOG.md` was refreshed.
- `.gitignore` now ignores only the root-level local binaries instead of accidentally hiding `build/home-cinema/...`.

## Updating
- The user data format did not change in this release.
- To update, replace the server binary or rebuild the `.app` from the current repository state.

## Assets
- `Home-Cinema-1.7-macOS-app.zip` — the ready-to-use macOS app bundle with the control UI and bundled server inside.
- `Home-Cinema-1.7-macOS-server.zip` — the standalone universal server binary for terminal or custom workflow use.
