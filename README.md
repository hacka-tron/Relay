# Relay: a program that updates itself

Relay is a small long-running program that keeps itself up to date. When a new version is published, every running copy downloads it, checks that it was really published by us, proves the new binary starts, swaps it into place and restarts, all without anyone touching the machine. If the new version keeps crashing, the program puts the old one back.

It is written in Go, builds to one static binary per platform, and runs on Linux, macOS and Windows. The repository also holds the update server and the release tool, because a self-updating program is only half of the system.

## Try it in one command

You need Go 1.26 or newer.

```sh
make demo          # macOS or Linux
.\scripts\demo.ps1 # Windows
```

The demo generates a signing key, builds and signs v1.0.0 and v1.1.0, starts the update server, starts the app at v1.0.0, promotes v1.1.0 and shows the app restarting as v1.1.0 under the same PID. It then publishes a v1.2.0 whose bytes were altered after signing and shows the app refusing it. Everything it creates lives in `./demo`, apart from the app's client ID, which goes to the user config directory (`~/Library/Application Support/relay` on macOS, `~/.config/relay` on Linux, `%AppData%\relay` on Windows).

## The desktop app

Each release on GitHub includes a downloadable app:

| Platform | Download | Notes |
|---|---|---|
| macOS 13 or later, Apple Silicon | `Relay-macos-arm64.zip` | unzip, drag `Relay.app` to Applications |
| macOS 13 or later, Intel | `Relay-macos-x86_64.zip` | same |
| Windows 10 and 11 | `app-windows-amd64.exe` | uses the WebView2 runtime, which current Windows ships with; older installs are offered a download |
| Linux, x86-64 and ARM | `app-linux-amd64`, `app-linux-arm64` | opens its page in a browser rather than a native window |

Move the Mac app out of Downloads before opening it. macOS runs apps opened straight from Downloads from a temporary read-only copy, where the updater cannot replace itself.

The builds are not signed with an Apple or Microsoft certificate (that needs paid accounts), so the first launch of a downloaded copy is blocked once. On macOS, try to open it, then choose Open Anyway under System Settings, Privacy and Security. On Windows, choose More info, then Run anyway. Updates after that are verified with Relay's own signatures and install silently.

Linux keeps the browser page on purpose: a native window there needs WebKitGTK and a recent glibc on every machine, and a server without them could never install an update. Every build accepts `--headless` to run as a background agent with no window or page at all.

**Settings.** At the bottom of the window are two settings, saved in `settings.json` beside the update state, so they survive restarts and updates:

- **Update server:** Cloud (the deployed server, built into release builds; its address is not shown in the window) or This computer (`http://localhost:8080`, for demos and development). Release builds start on Cloud.
- **Check every:** 10 seconds up to 1 hour. Default 1 minute.

A change triggers a check straight away, without a restart. Starting the app with `--server` or `--interval` overrides the saved setting for that run only (an address given with `--server` appears as a third choice, Custom, and is never saved). The page can only save the built-in choices, and only with a JSON request, which a page on another site cannot send to it.

The window shows the running version, a countdown to the next check, and a history of every update step (available, downloaded, installed, confirmed, rejected, rolled back). A character reacts to what the updater is doing: resting while idle, pinging with its antenna during a check, hopping while an update installs, and wincing when a download is refused. The Clear button empties the history. The history lives in `update-history.jsonl` so it survives the restart that every update causes. It and the updater's state file sit beside the executable, except in the macOS app, where they go to `~/Library/Application Support/relay` so the signed bundle is never modified apart from the executable itself.

**How it is built.** The window is [Wails](https://wails.io), which shows a web page in a native window using the operating system's own web engine (WebKit on macOS and Linux, WebView2 on Windows) rather than bundling a browser, so the app stays around 10 MB. It is compiled in only with the `desktop` build tag. The page is one embedded HTML file, and the window serves it from an HTTP handler inside the process, so no network port is opened.

Without the tag, `go build ./cmd/app` is still pure Go with no native toolchain. That build serves the same page on `127.0.0.1:4780` (loopback only, Host header checked) and opens a browser tab; `--ui` changes the address and `--open=false` keeps the browser closed. The tests and the demo scripts use this build.

**Updating an app bundle.** On macOS the updater replaces only `Relay.app/Contents/MacOS/relay`, so releases ship the bundle once, for first install, and every update after that is the bare binary, exactly as on the other platforms. The bundle is ad-hoc signed, and its `Info.plist` and icon are identical in every release, so the update binary, which CI takes from inside that release's signed bundle, keeps the installed bundle's signature valid. The restart `exec`s the new binary in the same process, so the window comes straight back.

## How it fits together

```
 developer / CI                   update server                    client machine
 --------------                   -------------                    --------------
 release build                    updateserver                     app
   compile 5 targets   upload       GET /v1/update?...  <-- check -- on start, then every N minutes
   sha256 each         ------->     decide by channel,  --- 200 --> verify signature (key compiled in)
   sign each                        percent, paused                 download to temp, check size + sha256
   write release.json                                               run new binary --selftest
                                    GET /files/...      <-- fetch -- swap atomically, keep .old
 release promote                                                    restart; roll back if it crash loops
   edit rollout.json
```

| Path | What it is |
|---|---|
| `cmd/app` | The program. Prints `hello from vX.Y.Z` every few seconds, runs the update loop, and serves a small window page on localhost. |
| `cmd/updateserver` | HTTP server: `/v1/update` decides what to offer, `/files/` serves binaries. |
| `cmd/release` | `keygen`, `build` (cross compile, hash, sign), `promote` (edit `rollout.json`). |
| `internal/manifest` | The signed description of one binary, and `Sign` / `Verify`. |
| `internal/rollout` | Pure decision logic: channel, percent, pause, never downgrade. |
| `internal/updater` | Client side: check, download with limits, verify, apply. |
| `internal/install` | Swap, rollback, self-test, crash-loop detection. Per-OS pieces behind build tags. |

## Trust: who can ship an update

Every release is signed with an Ed25519 private key that exists only on the release machine or in the CI secret. The matching public key is compiled into the app. The update server and the file storage never see the private key. So if the server, the storage or the network between them is compromised, an attacker can stop updates from arriving but cannot make a client install anything of their own.

What is signed is not the JSON manifest but a short fixed text:

```
updater-release-v1
version=1.2.0
os=linux
arch=amd64
sha256=<hex of the binary>
```

Signing a fixed text avoids every question about JSON key order or whitespace. Binding the version in means an old, genuinely signed binary cannot be replayed as a newer one. Binding os and arch in means a signed Windows build cannot be served to a Linux client. The client checks, in this order, before installing anything:

1. the signature is valid for the compiled-in public key
2. the manifest is for this machine's os and arch
3. the version is strictly newer than the running one (semver), so no downgrades
4. the download URL is http or https and the declared size is within the limit
5. after downloading, the byte count and SHA-256 match the manifest exactly
6. the new binary runs `--selftest`, exits 0 and prints its version within 10 seconds

Only then is the binary swapped in.

## Reliability: what happens when things go wrong

- **Interrupted download.** It goes to a temp file next to the executable and is deleted on any mismatch. Leftover temp files are cleaned on the next start.
- **Crash during the swap.** The last step is a single `rename`, which the OS makes atomic, so the executable path never holds a half written program. On Linux and macOS it always holds a complete program; on Windows there is a brief window between two renames (see below) in which it is absent, and a failed second rename puts the old program back.
- **The new version will not start.** The self-test catches this before the swap. If a version passes the self-test but then crashes repeatedly, the start counter in `update-state.json` notices: after two failed starts the third start rolls back to the `.old` binary and records the version so it is never installed again on that machine (the server may keep offering it; the client refuses). This relies on something restarting the program after a crash, see assumption 1 below. After 30 seconds of healthy running (`--confirm-after`) the update is confirmed and `.old` is deleted.
- **Two copies updating at once.** A lock file next to the executable prevents it. A lock older than ten minutes is treated as left by a crash.
- **An update arrives before the previous one is confirmed.** It waits. Otherwise `.old` would be overwritten with a version that never proved itself.
- **A corrupt state file.** It is moved aside and treated as empty. State writes are atomic (write temp, rename) because they happen right after an update, when crashes are most likely.
- **Server unreachable, bad response, bad manifest.** Logged and retried at the next interval. The program never exits because of the updater.
- **Fleet polling.** Each check is jittered by up to 20 percent of the interval so clients do not line up.
- **Operator kill switch.** `paused: true` in `rollout.json` makes the server answer "nothing for you" to everyone, effective on the next request with no restart.

### Per-OS details

- **Linux and macOS.** The running program is copied to `app.old`, then the new file is renamed over `app`. The program then `exec`s itself, keeping the same PID, arguments and environment.
- **Windows.** A running executable cannot be overwritten or deleted, but it can be renamed. So `app.exe` is renamed to `app.exe.old`, the new file is renamed to `app.exe`, a new process is started and the old one exits. A rolled-back binary is renamed to `.bad` and deleted on the next start, for the same reason.

## The update server

```
GET /v1/update?client_id=<id>&version=<current>&os=<goos>&arch=<goarch>&channel=<stable|beta>
  204  nothing to do
  200  {"version":"1.2.0","os":"linux","arch":"amd64","url":"...","sha256":"...","size":123,"signature":"..."}
GET /files/<version>/<binary>
GET /health
```

The server reads `releases/rollout.json` on every request:

```json
{
  "channels": {
    "stable": { "version": "1.1.0", "rollout_percent": 100, "paused": false },
    "beta":   { "version": "1.2.0", "rollout_percent": 25,  "paused": false }
  }
}
```

A client is inside a percentage rollout when `fnv32(client_id + version) % 100 < rollout_percent`. The hash is deterministic, so a client does not flip in and out of a rollout between checks, and it includes the version so each release reshuffles who goes first. The server never offers a version that is not strictly newer than what the client reports. All decision logic is in `internal/rollout`, which has no I/O, so new rules (by OS, by an allowlist of client IDs, a minimum required version) are one more `if` plus a test.

Client facts are sent on every check from day one, so most new server-side rules need no client change.

Binaries are served from the `releases/` directory by the same process. `release.json` stores just a file name and the server prefixes its own `/files/<version>/` path. If `release.json` holds an absolute URL instead, the server passes it through unchanged, so moving binaries to object storage or GitHub Releases is a change to the release tool only.

### Operator page

With `ADMIN_TOKEN` set (16 characters or more), the server also serves `/admin` behind HTTP basic auth (any username, the token as password). It shows each channel with its version, percentage and pause switch, the releases on disk, and the clients that checked in recently with the version they are running. It can also import a release that CI published to GitHub, given `GITHUB_REPO` (and `GITHUB_TOKEN` for a private repository), which is the step between "tagged" and "promotable". Everything the page does, `release promote` does from the command line.

## Shipping a release

```sh
go run ./cmd/release keygen                      # once; commit pubkey.txt, never signing.key
go run ./cmd/release build --version 1.2.0       # releases/1.2.0/{app-*, release.json}
go run ./cmd/release build --version 1.2.0 --desktop --targets darwin/arm64   # with the native window
go run ./cmd/release build --version 1.2.0 --cloud-server https://updates.example.com   # default the app to a deployed server
go run ./cmd/release promote --version 1.2.0 --channel beta --percent 10
go run ./cmd/release promote --version 1.2.0     # stable, 100 percent
go run ./cmd/release promote --version 1.2.0 --paused   # kill switch
```

Building and promoting are separate on purpose: a build can sit in `releases/` for as long as you like before any client hears about it.

In CI, pushing a tag such as `v1.2.0` runs `.github/workflows/release.yml`, which compiles each platform on a matching runner (`release compile`, which needs only the public key), then signs everything in one final job with the `UPDATER_SIGNING_KEY` repository secret (`release sign`), and uploads the binaries, the macOS app zips and `release.json` to a GitHub Release. Splitting compile from sign means the private key is exposed to exactly one job. To serve a CI-built release from a local server, pull it down with `make fetch VERSION=1.2.0` and then `promote` it.

## Assumptions

The brief left some things open. These are the answers the design assumes.

1. **What kind of program is it?** A long-running agent or daemon, run under a supervisor (systemd, launchd, a Windows service) that restarts it if it exits. Crash-loop rollback depends on that restart; without a supervisor a crashing version simply stays down. On Windows the restart after an update hands off to a new process, so a service wrapper would instead let the service manager perform the restart. Updates are applied silently. For an interactive app the same code would gain a prompt-before-restart mode; for managed fleets, operator pinning.
2. **Who runs it?** End-user machines we do not control, possibly offline for long stretches. Hence the retry-forever loop and the strictness about never leaving a broken install behind.
3. **OS code signing and notarization?** Out of scope: Gatekeeper and SmartScreen need paid certificates. Our own signatures cover update integrity; OS signing would be added at build time in `release build`.
4. **Package managers?** Out of scope. A copy installed by brew, apt or winget should defer to that manager; the natural hook is a build-time flag that disables the updater.
5. **Rollback?** Required, and automatic.
6. **Hosting?** The server runs locally for the demo and is also deployed to Cloud Run. It is a stateless binary reading a directory, so it deploys unchanged to Cloud Run, Fly.io or a container anywhere (`Dockerfile`, set `BASE_URL` to the public address). On Cloud Run the `releases/` directory is a Cloud Storage bucket mounted as a volume, so imports and channel changes made on the operator page persist across restarts:

   ```sh
   gcloud run deploy relay-updateserver --source . --region us-east1 --allow-unauthenticated \
     --max-instances 1 --env-vars-file run-env.yaml \
     --add-volume name=releases,type=cloud-storage,bucket=<bucket> \
     --add-volume-mount volume=releases,mount-path=/data
   ```

   with `run-env.yaml` holding `BASE_URL`, `RELEASES_DIR: /data`, `ADMIN_TOKEN` and `GITHUB_REPO`. After that first deploy, `.github/workflows/deploy.yml` redeploys automatically on every push to `main` that touches the server, keeping the environment and bucket mount from the previous revision. It authenticates with Workload Identity Federation, set up once by `scripts/setup-deploy-identity.sh`, so no cloud key is stored in GitHub. The client list on the operator page is kept in memory per instance, which is why the demo runs one instance. Serve binaries from a CDN at scale.

## Threat model and limits

- Transport is plain HTTP in the demo. Production must use HTTPS; signatures protect integrity either way, HTTPS additionally hides which clients run which versions.
- A compromised server can withhold updates or keep serving an old release. It cannot forge one. Preventing an indefinite freeze would need signed, expiring manifests as in TUF, which is noted as future work.
- The private key is a single point of trust. Rotation would ship a list of trusted public keys in the client and sign new releases with the new key while old clients still accept the old one.
- Download size is capped, every network call has a timeout, and the server refuses to serve anything outside `releases/<version>/<file>`.

## Out of scope

OS code signing, delta updates, a prompt-before-restart UI, enterprise approval windows, an admin UI or authentication for editing `rollout.json`, and a full TUF implementation.

## Development

```sh
make check   # gofmt, go vet, go build
make test    # go test ./...
```

Dependencies outside the standard library: `golang.org/x/mod/semver` for version comparison, and Wails for the desktop window, which only the `desktop` build tag pulls in. The updater, server and release tool use neither.
