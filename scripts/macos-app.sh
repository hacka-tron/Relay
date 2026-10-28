#!/usr/bin/env bash
# Wraps a desktop build of the app in a signed macOS .app bundle and zips it.
#
#   scripts/macos-app.sh <binary> <out-dir>
#
# Produces <out-dir>/Relay.app and <out-dir>/Relay-macos-<arch>.zip, and
# overwrites <binary> with the executable as signed inside the bundle.
#
# Why that last step: the updater only ever replaces Contents/MacOS/relay,
# so later updates ship as the bare binary. macOS checks that executable's
# signature against the bundle around it (Info.plist and resources). The
# Info.plist below is identical in every release, and so is the icon, so
# an executable signed inside this release's bundle is equally valid inside
# a bundle installed from any earlier release. The running version shows in
# the window and the About box, not in Finder.
set -euo pipefail
BIN=$1; OUT=$2
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
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Ad-hoc signature (no paid certificate): seals Info.plist and resources so
# a downloaded copy can be allowed in System Settings instead of being
# reported as damaged.
codesign --force --sign - --identifier com.hacka-tron.relay "$APP"
codesign --verify --strict "$APP"
cp "$APP/Contents/MacOS/relay" "$BIN"

(cd "$OUT" && rm -f "Relay-macos-$ARCH.zip" && ditto -c -k --keepParent Relay.app "Relay-macos-$ARCH.zip")
echo "$APP"
echo "$OUT/Relay-macos-$ARCH.zip"
