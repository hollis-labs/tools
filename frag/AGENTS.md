# Frag

Frag is a macOS menu-bar quick-capture window. A global hotkey opens it, you
type text plus comma-separated tags, and Cmd+Enter POSTs them to a local
Fragments Engine intake endpoint. It does not read, search, browse or sync
fragments, has no offline queue, and keeps no state beyond the hotkey it
remembers in `UserDefaults`.

## Start Here

`main.swift` is the entire app, sectioned by `// MARK:` comments. Grep for the
section name; line numbers move.

- `MARK: - Config` — intake URL and default hotkey. The only configuration.
- `MARK: - API` — `IntakePayload` and `IntakeResponse` are the whole contract
  with the engine.
- `FragWindowController` — builds the window, owns show/hide, submit and the
  status line.
- `AppDelegate` — status item, Carbon hotkey registration, set-hotkey sheet.
- `build.sh` compiles and installs the bundle. `Info.plist` carries
  `LSUIElement`, the local-networking exception and the macOS 13 floor.

## Commands

```bash
./build.sh
open ~/Applications/Frag.app
```

There is no Xcode project and no test suite — `build.sh` runs `swiftc` over the
one file, then assembles the bundle. Verification is running the app against a
live engine on `localhost:8091`. `build.sh` deletes and rewrites
`~/Applications/Frag.app`, so quit a running Frag before rebuilding.

## Boundaries

Esc, Cmd+Enter, Cmd+C and Cmd+V are handled in four places on purpose:
`FragWindow.keyDown`, `FragTextView`, `TagsTextField`, and
`control(_:textView:doCommandBy:)` on the controller. A field editor swallows
these before the window sees them, and the app sets no `NSApp.mainMenu`, so
there is no Edit menu to supply a default clipboard responder. Do not collapse
them into one path, and exercise both the text area and the tags field after
touching any of them.

The window hides on `windowDidResignKey`. Anything that opens a sheet, alert or
panel must set `suppressHide` around it the way `presentSetHotkey` does, or the
window vanishes the moment it appears.

The hotkey is a Carbon `RegisterEventHotKey`, not an NSEvent monitor. That is
why `build.sh` links `Carbon` and why `unregisterHotkey` on terminate matters.

`kAPIBase` is cleartext `http://localhost:8091`, permitted only by
`NSAllowsLocalNetworking` in `Info.plist`. Pointing Frag at a non-local host
needs an ATS change as well as a new URL.
