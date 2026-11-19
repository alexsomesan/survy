# survy macOS menu bar app

A small menu bar icon for macOS that opens the survy dashboard in its own window, with no location bar or toolbar. It is a single Swift file wrapping a `WKWebView`; it is not part of the router image and survy does not depend on it.

- **Left-click** the icon: open the dashboard window (a second click brings it to the front).
- **Right-click**: open UniFi Network in its own window, or quit Survy.
- Each window remembers its size and position and keeps cookies between launches. Drag it by the slim title bar. ⌘W closes, ⌘R reloads the active window, ⌘Q quits.
- Links that would open a new tab load in the same window.

## Build and install

Needs the Xcode command line tools (`swiftc`).

```sh
./build.sh            # builds and installs ~/Applications/Survy.app
./build.sh --login    # the same, plus start at login (a LaunchAgent)
open ~/Applications/Survy.app
```

The default dashboard address, `https://survy.example.com/`, is a placeholder. Set `SURVY_URL` in the app's environment to your dashboard URL, or edit the `url` constant at the top of `Survy.swift` before building.

Quit the app before rebuilding. To remove start at login:

```sh
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/de.somesan.survy-menubar.plist
rm ~/Library/LaunchAgents/de.somesan.survy-menubar.plist
```

The app is ad-hoc signed and is meant for the machine that built it. Like survy itself it has no login or HTTPS and assumes the Trusted LAN.

### UniFi Network shortcut

**Open UniFi Network** only uses the URL in `~/Library/Application Support/Survy/config.json`; there is no default. Create the `Survy` directory if needed, then create the file:

```json
{
  "unifiURL": "https://unifi.example.com:8443/"
}
```

Use an absolute `http://` or `https://` URL. Quit and reopen Survy to apply changes; no rebuild is needed. If the file is missing, cannot be read or contains an invalid setting, the shortcut is disabled and Survy logs the error.
