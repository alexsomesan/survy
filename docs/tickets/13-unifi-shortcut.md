# 13: UniFi Network shortcut

Status: open
Blocked by: none
Spec: [0003](../spec/0003-unifi-shortcut.md)

## Scope

Add a browser shortcut to the macOS status icon's existing right-click menu and document the per-user configuration.

## Acceptance criteria

Smoke-check on macOS:

- With no config file, **Open UniFi Network** opens the default URL in the default browser.
- With a different absolute HTTP(S) URL in the config, relaunching the app makes the shortcut open that URL, including its port and path.
- Malformed JSON or an invalid URL disables the shortcut and logs why. Left-click still opens the Survy dashboard and Quit Survy still works.

## Results

`make test` passed on Linux. The macOS build and menu smoke check are still unverified.
