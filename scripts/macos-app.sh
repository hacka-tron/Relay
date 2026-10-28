#!/usr/bin/env bash
# Wraps a desktop build of the app in a macOS .app bundle and zips it.
#
#   scripts/macos-app.sh <binary> <version> <out-dir>
#
# Produces <out-dir>/Relay.app and <out-dir>/Relay-macos-<arch>.zip. The
# updater only ever replaces Contents/MacOS/relay, so later updates ship as
# the bare binary; this bundle is for the first install.
set -euo pipefail
BIN=$1; VERSION=$2; OUT=$3
ARCH=$(lipo -archs "$BIN")
APP="$OUT/Relay.app"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
rm -rf "$APP"; mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/relay"
chmod +x "$APP/Contents/MacOS/relay"

ICONSET=$(mktemp -d)/icon.iconset; mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$ROOT/build/icon.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$ROOT/build/icon.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/icon.icns"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Relay</string>
  <key>CFBundleDisplayName</key><string>Relay</string>
  <key>CFBundleIdentifier</key><string>com.hacka-tron.relay</string>
  <key>CFBundleExecutable</key><string>relay</string>
  <key>CFBundleIconFile</key><string>icon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>11.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

(cd "$OUT" && rm -f "Relay-macos-$ARCH.zip" && ditto -c -k --keepParent Relay.app "Relay-macos-$ARCH.zip")
echo "$APP"
echo "$OUT/Relay-macos-$ARCH.zip"
