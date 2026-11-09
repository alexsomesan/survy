# survy macOS menu bar app

A small menu bar icon for macOS that opens the survy dashboard in its own window, with no location bar or toolbar. It is a single Swift file wrapping a `WKWebView`; it is not part of the router image and survy does not depend on it.

- **Left-click** the icon: open the dashboard window (a second click brings it to the front).
- **Right-click**: Quit.
- The window remembers its size and position and keeps cookies between launches. Drag it by the slim title bar. ⌘W closes, ⌘R reloads, ⌘Q quits.
- Links that would open a new tab load in the same window.

## Build and install

Needs the Xcode command line tools (`swiftc`).

```sh
./build.sh            # builds and installs ~/Applications/Survy.app
./build.sh --login    # the same, plus start at login (a LaunchAgent)
open ~/Applications/Survy.app
```

The dashboard address defaults to the owner's (`https://survy.home.somesan.de/`) and is the `url` constant at the top of `Survy.swift`. Edit it, or set `SURVY_URL` in the app's environment, to point elsewhere.

Quit the app before rebuilding. To remove start at login:

```sh
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/de.somesan.survy-menubar.plist
rm ~/Library/LaunchAgents/de.somesan.survy-menubar.plist
```

The app is ad-hoc signed and is meant for the machine that built it. Like survy itself it has no login or HTTPS and assumes the Trusted LAN.
