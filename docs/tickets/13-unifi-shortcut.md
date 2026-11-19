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

`make test` passed on Linux. The macOS build and menu smoke check are still unverified.
