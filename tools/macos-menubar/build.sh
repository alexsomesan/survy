#!/bin/sh
# Build and install Survy.app into ~/Applications. Usage: ./build.sh [--login]
set -e
cd "$(dirname "$0")"
APP="$HOME/Applications/Survy.app"
mkdir -p "$APP/Contents/MacOS"
swiftc -O Survy.swift -o "$APP/Contents/MacOS/Survy"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Survy</string>
<key>CFBundleIdentifier</key><string>de.somesan.survy-menubar</string>
<key>CFBundleExecutable</key><string>Survy</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>LSUIElement</key><true/>
</dict></plist>
PLIST
codesign --force --sign - "$APP"

if [ "$1" = "--login" ]; then
  P="$HOME/Library/LaunchAgents/de.somesan.survy-menubar.plist"
  mkdir -p "$HOME/Library/LaunchAgents"
  cat > "$P" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>de.somesan.survy-menubar</string>
<key>ProgramArguments</key><array><string>$APP/Contents/MacOS/Survy</string></array>
<key>RunAtLoad</key><true/>
<key>ProcessType</key><string>Interactive</string>
</dict></plist>
PLIST
  launchctl bootout "gui/$(id -u)" "$P" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$P"
fi
echo "Installed $APP"
