#!/usr/bin/env bash
# End to end demo on macOS or Linux. Everything lands in ./demo (gitignored).
#
#  1. generate a signing key
#  2. build and sign v1.0.0 and v1.1.0 for this machine
#  3. start the update server and the app at v1.0.0
#  4. promote v1.1.0 and watch the app update itself
#  5. publish a tampered v1.2.0 and watch the app refuse it
set -euo pipefail
cd "$(dirname "$0")/.."

DEMO=${DEMO_DIR:-$PWD/demo}
PORT=${PORT:-8080}
HOST=$(go env GOOS); ARCH=$(go env GOARCH)
BIN="app-$HOST-$ARCH"
rm -rf "$DEMO"; mkdir -p "$DEMO/releases" "$DEMO/bin"

say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
rel()  { go run ./cmd/release "$@"; }
build(){ rel build --version "$1" --key "$DEMO/signing.key" --out "$DEMO/releases" --targets "$HOST/$ARCH" >/dev/null; }
promote(){ rel promote --version "$1" --releases "$DEMO/releases"; }
wait_for() { # wait_for <pattern> <file> <seconds>
  local i; for ((i=0; i<$3; i++)); do grep -q "$1" "$2" && return 0; sleep 1; done
  echo "timed out waiting for: $1"; tail -20 "$2"; return 1
}

say "keygen"
rel keygen --out "$DEMO"

say "build v1.0.0 and v1.1.0"
build 1.0.0; build 1.1.0
promote 1.0.0

say "start update server on :$PORT"
go build -o "$DEMO/updateserver" ./cmd/updateserver
"$DEMO/updateserver" -addr "127.0.0.1:$PORT" -releases "$DEMO/releases" -base-url "http://127.0.0.1:$PORT" >"$DEMO/server.log" 2>&1 &
SERVER=$!
APP=
trap 'kill $SERVER ${APP:-} 2>/dev/null || true' EXIT

say "install and start app v1.0.0"
cp "$DEMO/releases/1.0.0/$BIN" "$DEMO/bin/app"
"$DEMO/bin/app" --server "http://127.0.0.1:$PORT" --interval 5s >"$DEMO/app.log" 2>&1 &
APP=$!
wait_for "hello from v1.0.0" "$DEMO/app.log" 10
tail -1 "$DEMO/app.log"

say "promote v1.1.0; the app should pick it up within a few seconds"
promote 1.1.0
wait_for "hello from v1.1.0" "$DEMO/app.log" 30
grep -E "update available|installed|hello from v1.1.0" "$DEMO/app.log" | head -4
echo "same PID after restart: $APP = $(pgrep -f "$DEMO/bin/app" | head -1)"
ls "$DEMO/bin"

say "publish a tampered v1.2.0 (bytes changed after signing)"
build 1.2.0
printf 'X' | dd of="$DEMO/releases/1.2.0/$BIN" bs=1 seek=4096 conv=notrunc 2>/dev/null
promote 1.2.0
wait_for "sha256 does not match" "$DEMO/app.log" 30
grep "sha256 does not match" "$DEMO/app.log" | head -1
tail -1 "$DEMO/app.log"

say "done: app is still running v1.1.0 and refused the tampered build"
