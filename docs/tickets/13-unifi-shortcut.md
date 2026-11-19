# 13: UniFi Network shortcut

Status: open
Blocked by: none
Spec: [0003](../spec/0003-unifi-shortcut.md)

## Scope

Add a UniFi Network shortcut to the macOS status icon's existing right-click menu and document the per-user configuration.

## Acceptance criteria

Smoke-check on macOS:

- With no config file, **Open UniFi Network** is disabled and Survy logs why; there is no default URL.
- With an absolute HTTP(S) URL in the config, relaunching the app makes the shortcut open that URL, including its port and path, in its own system webview window. Reopening it brings the window to the front without replacing the dashboard.
- Both windows retain their own size and position; ⌘R reloads the active window.
- Malformed JSON or an invalid URL disables the shortcut and logs why. Left-click still opens the Survy dashboard and Quit Survy still works.

## Results

`make test` passed on Linux (reported by the prior verification; not rerun here).

macOS verification on 2026-11-19:

- `git fetch origin` succeeded. The isolated worktree and
  `origin/alexsomesan-supreme-adventure` both pointed to
  `4f9ac3a51429c13d18a6e31a23d2d9167d77d620`; the primary checkout was not used.
- `tools/macos-menubar/build.sh` without `--login` succeeded on macOS 27.2.1
  (arm64), using Apple Swift 6.4. The first build ran while Survy was running;
  after the user confirmed quitting, a process check confirmed it was stopped
  and the build was repeated successfully. No Swift fixes were needed.
- The existing per-user config file was present and was not changed. After the
  user opened the app and left-clicked its icon, Computer Use observed the
  dashboard loaded inside Survy. Sending Cmd-R visibly reset the dashboard to
  its initial Connecting state, confirming dashboard reload.
- The user reported that right-click > Open UniFi Network opened a separate
  UniFi window and left the dashboard intact. Initially Computer Use exposed
  only the dashboard window. On follow-up after the user reported a blank
  window, Computer Use observed two Survy windows; the second had no web
  content, and WebKit logged a main-frame certificate failure (`-1202`).
- The configured UniFi endpoint used HTTPS port 8443, which served a
  self-signed certificate with subject `CN=UniFi` and SAN `DNS:UniFi`, not the
  configured hostname. The same hostname on port 443 served a valid Let's
  Encrypt wildcard certificate and returned HTTP 200 with certificate
  validation enabled. Thus the blank-window check did not establish a
  successful UniFi page load. The config was left unchanged; removing the
  explicit port 8443 and relaunching remains an owner action before retesting.
  No certificate bypass or app behavior change was introduced.
- Reopening either window, independent saved frames, Cmd-R in the UniFi window,
  and disabled-menu/logging behavior for missing config, malformed JSON or an
  invalid URL remain unverified. Computer Use could not bind Survy while it
  exposed only its menu-bar icon, and its status menu was not available in the
  bound dashboard's accessibility tree.

The ticket remains open pending the remaining macOS acceptance smoke checks.
