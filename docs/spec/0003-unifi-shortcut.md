# Spec 0003: UniFi Network shortcut

The optional macOS menu bar app should offer **Open UniFi Network** on right-click, so the owner can reach the UniFi Network UI without opening the Survy dashboard first. The shortcut opens in the default browser. Left-click still opens the Survy dashboard, and the menu still offers Quit Survy.

The URL is loaded only from `unifiURL` in `~/Library/Application Support/Survy/config.json`; there is no default. Set it to `https://unifi-ctrl.home.somesan.de:8443/` for the owner's network and relaunch the app. A missing, invalid or unreadable file disables the shortcut and logs the error rather than opening a different address.

This only changes the local macOS app; it needs no router or dashboard API changes. See [ticket 13](../tickets/13-unifi-shortcut.md).
