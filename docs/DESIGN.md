# Design notes

The README explains how to run and use Relay. This document records the decisions behind it and the details of each component, so the reasoning is not lost when the code changes.

## The task

Write a program that updates itself. Deployed copies should be seamlessly replaced when a new version ships. Production-quality code, any common language, must work on Windows, macOS and Linux, including any non-trivial server components. Where the task leaves a question open, write it down, pick an answer and proceed.

## Key decisions

| Area | Decision | Why |
|---|---|---|
| Language | Go for client, server and release tool | The client compiles to one small native binary with no runtime, cross-compiles to all three OSes, and Ed25519 and SHA-256 are in the standard library. One language across the stack. |
| Server | Small Go HTTP server, run locally for the demo | Lets us add rollout rules (canary percent, pause, channels). Anyone can run it with one command. The same code deploys to Cloud Run or Fly.io unchanged. |
| Binary storage | Local `releases/` folder served by the same server | Stand-in for S3 or GitHub Releases. The client just follows the download URL in the manifest, so swapping storage is a release tool change only. |
| Trust | Ed25519 signatures. Private key only in the release tool or the CI secret. Public key compiled into the client. | Even if the server or storage is compromised, an attacker cannot produce a valid release. |
| Demo app | Prints `hello from vX.Y.Z` every few seconds | The app only exists to prove the updater works. Easy to see on a screen. |
| CI | GitHub Actions workflow that calls the same release tool a developer runs locally | Shows the real production path without making the demo depend on it. |

## Assumed answers to open questions

1. **What kind of program?** A long-running program, either a desktop app or a background agent (`--headless`). Updates are silent and applied automatically. Crash-loop rollback relies on the program being started again after a crash, by the user or by a supervisor such as systemd, launchd or a Windows service. Without that, a crashing version simply stays down until the next launch, which then rolls it back.
2. **Who are the clients?** End-user machines we do not control, possibly offline for long periods.
3. **Silent or prompted?** Silent for this program. A prompt-to-restart mode for interactive apps and operator-controlled pinning for enterprise are natural extensions.
4. **Code signing for Gatekeeper and SmartScreen?** Out of scope (needs paid certificates). Our own Ed25519 signatures cover update integrity.
5. **Package managers (brew, apt, winget)?** Out of scope. If installed by a package manager, the app should defer to it.
6. **Rollback?** Yes, assumed required.
7. **Server hosting?** Runs locally for the demo, and is deployed to Cloud Run (see Hosting below). At scale, binaries would move to a CDN or GitHub Releases, which the manifest's absolute URL support already allows.

## Architecture

```
 developer / CI                    update server                       client (user machine)
 ---------------                   -------------                       ---------------------
 cmd/release                       cmd/updateserver                    cmd/app
  build 5 targets      upload       GET /v1/update?...   <---- check --  on start + every N min (jitter)
  sha256 each binary  -------->     pick version by rules  ---- 200 -->  verify signature (embedded pubkey)
  sign with private key             (channel, rollout %,                 download -> temp file
  write release.json                 paused)                             verify sha256 + size
  promote (rollout.json)            GET /files/...        <-- fetch --   self-test new binary
                                                                         atomic swap, keep .old
                                                                         restart, confirm healthy or roll back
```

### What gets signed

The release tool signs a small canonical message per binary, not the JSON, which avoids canonicalization bugs:

```
updater-release-v1
version=1.2.0
os=linux
arch=amd64
sha256=<hex>
```

Binding version, os and arch into the signature stops an attacker from replaying an old signed binary as a "new" version or serving a binary for the wrong platform. The URL and size are not signed: the URL may change when storage moves, and the hash already pins the exact bytes. The manifest JSON carries os and arch too, so the signed message can be rebuilt from the manifest alone.

### Update check protocol

`GET /v1/update?client_id=<id>&version=<cur>&os=<goos>&arch=<goarch>&channel=<stable|beta>`

- `204 No Content`: no update for you.
- `200 OK` with JSON:
  ```json
  {
    "version": "1.2.0",
    "os": "linux",
    "arch": "amd64",
    "url": "http://localhost:8080/files/1.2.0/app-linux-amd64",
    "sha256": "<hex>",
    "size": 2345678,
    "signature": "<base64 ed25519 sig over the canonical message>"
  }
  ```

The client sends a flexible set of facts from day one (id, version, os, arch, channel) so new server-side rules rarely need a client change.

### Server rollout rules

`releases/rollout.json`, re-read on every request so changes take effect instantly with no deploy:

```json
{
  "channels": {
    "stable": { "version": "1.1.0", "rollout_percent": 100, "paused": false },
    "beta":   { "version": "1.2.0", "rollout_percent": 25,  "paused": false }
  }
}
```

- Bucketing: `fnv32(client_id + target_version) % 100 < rollout_percent`. Deterministic, so a client does not flip in and out of the rollout, and a new version reshuffles who goes first.
- `paused: true` returns 204 for everyone (kill switch).
- Never offer a version that is not strictly newer than the client's.
- The decision is a pure function, `rollout.Decide(facts, config) (version, ok)`, with no I/O. The server then loads that version's `release.json` and picks the platform target. Adding a rule (by OS, by client allowlist, a minimum required version) is one more condition in `Decide` plus a test.

## Repository layout

```
.
├── cmd/
│   ├── app/            # the client: demo app + updater wiring
│   ├── updateserver/   # update check API + static file serving
│   └── release/        # keygen, build (compile, hash, sign), promote
├── internal/
│   ├── manifest/       # types, canonical signing message, Sign/Verify, key encoding
│   ├── rollout/        # server-side decision logic (pure, easy to test)
│   ├── updater/        # check, download, verify, apply
│   ├── install/        # swap, rollback, self-test, crash-loop state; per-OS files via build tags
│   └── atomicfile/     # write-then-rename helper
├── releases/           # gitignored except rollout.json
├── cmd/demo/           # local demo: start, next, bad (also run by CI on every OS)
├── scripts/            # macOS bundle, icon, one-time cloud setup
├── .github/workflows/  # ci.yml (3 OS test matrix), release.yml (tag push builds and publishes)
├── Dockerfile          # for the server
└── Makefile
```

## Client behavior in detail

1. **Version** injected at build time: `-ldflags "-X main.version=1.2.0"`.
2. **Public key** injected the same way: `-X main.publicKey=<base64>`. A plain `go build` leaves it empty, which disables the updater.
3. **Client ID**: random UUID generated on first run, stored in the OS config dir (`os.UserConfigDir()`).
4. **Check loop**: on start, then every interval (flag, default 1 min) with up to 20 percent jitter. HTTP timeout. Errors are logged and retried, never fatal.
5. **Download**: to a temp file in the same directory as the executable (same filesystem, so rename is atomic). Enforce a size cap and the manifest's `size`. Stream through SHA-256 while writing.
6. **Verify**, in order, reject on any failure:
   - signature valid for the canonical message (embedded public key)
   - os and arch match this machine
   - version is strictly greater than current (semver), no downgrades
   - URL is http or https, size within the cap
   - after download, byte count and sha256 match the manifest
7. **Self-test before swap**: run the new binary with `--selftest`. It must exit 0 and print its version within 10 seconds. Catches broken builds before they replace anything.
8. **Swap** (`internal/install`):
   - Unix: copy the current exe to `app.old`, then `os.Rename(new, exe)` (atomic). Preserve executable mode.
   - Windows: a running exe cannot be overwritten but can be renamed. Rename `app.exe` to `app.exe.old`, then rename the new file to `app.exe`. If the second rename fails, put the old one back.
9. **Restart**:
   - Unix: `syscall.Exec` into the new binary (same PID).
   - Windows: start the new process, then exit.
10. **Post-swap health and rollback**: before the swap, record the new version in `update-state.json` as pending. On startup, if an update is pending, count the start; after two failed starts the third start rolls back to `.old` and records the version so it is never installed again. After running healthy for `--confirm-after` (default 30 s), clear the marker and delete `.old`. An update that arrives while another is unconfirmed waits.
11. **Housekeeping**: on start, clean up stray temp files and a leftover `.bad` binary. A lock file stops two instances from updating at once; a lock older than ten minutes is treated as stale.
12. **Flags**: `--server`, `--channel`, `--interval`, `--confirm-after`, `--ui`, `--open`, `--headless`, `--selftest`, `--version`.
13. **Window**: one embedded HTML page polling `/api/state`. Built with the `desktop` tag it is shown in a native Wails window and served in-process; without the tag it is served on `--ui` (default 127.0.0.1:4780) and opened in a browser. History is appended to `update-history.jsonl` next to the binary.

## Release tool

- `release keygen` writes `signing.key` (private, gitignored) and `pubkey.txt`.
- `release compile` builds binaries with only the public key; `release sign` hashes and signs whatever binaries are in `releases/<version>`. CI compiles per platform and signs in one job.
- `release build --version 1.2.0`:
  - cross-compiles linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 with version and public key injected
  - computes sha256 and size for each
  - signs each canonical message with the private key from `UPDATER_SIGNING_KEY` or `--key`
  - writes binaries plus `releases/1.2.0/release.json`
- `release promote --version 1.2.0 --channel stable --percent 100` edits `rollout.json`. Building and promoting are separate, deliberate steps.

## Security checklist

- HTTPS in production (the demo uses localhost HTTP, documented).
- Ed25519 signature over version, os, arch and sha256; public key pinned in the binary.
- Private key never on the server; only on the release machine or in CI secrets.
- No downgrades.
- Size limits and timeouts on downloads; only http and https URLs.
- Server compromise cannot ship malware, only withhold updates (freeze attack: a known limitation; the mitigation would be signed manifests with expiry, as in TUF).
- Key rotation: future work (ship multiple trusted public keys).

## Reliability checklist

- Atomic rename, temp file on the same filesystem, atomic state file writes.
- Self-test before swap; crash-loop rollback after swap.
- Bad versions recorded and skipped.
- An interrupted download or swap leaves the old version working; leftovers are cleaned on the next start.
- Jittered polling so clients do not stampede the server.
- Kill switch via `paused`.

## Out of scope

- OS code signing and notarization (Gatekeeper, SmartScreen).
- Delta or patch updates.
- Prompt-to-restart UI.
- Enterprise pinning and approval windows.
- Admin UI or auth for changing `rollout.json`.
- Full TUF.

## Desktop app

The window is [Wails](https://wails.io), which shows a web page in a native window using the operating system's own web engine (WebKit on macOS, WebView2 on Windows) instead of bundling a browser, so the app stays around 10 MB. Wails is compiled in only with the `desktop` build tag. Without it, `go build ./cmd/app` is pure Go with no native toolchain, serves the same page on `127.0.0.1:4780` (loopback only, Host header checked) and opens a browser tab. The tests, the demo scripts and the Linux release use this build. A native window on Linux would need WebKitGTK and a recent glibc on every machine, and a server without them could never install an update.

The page is one embedded HTML file that polls `/api/state` once a second. In the desktop build it is served from an HTTP handler inside the process, so no port is opened. The only two write endpoints, the check interval and clear history, accept only a JSON request, which a page on another site cannot send, and the interval must be one of the offered choices.

Where updates come from is fixed when the app is built. Release builds have the cloud server compiled in (`release compile --cloud-server`), and a build without one, such as the demo, uses `http://localhost:8080`. There is deliberately no switch in the window: two servers that can each offer a version with the same number would be confusing, and a user who picked the wrong one would silently stop getting updates. `--server` overrides the address for one run, for testing.

The check interval lives in `settings.json`, and the update history in `update-history.jsonl`, in the state directory. That is beside the executable, except inside a macOS app bundle, where it is `~/Library/Application Support/relay`. Every file under `Relay.app/Contents` is covered by the bundle's signature, so the updater never leaves anything there except the executable itself. `--interval` overrides the saved interval for one run and is never saved.

**macOS bundles.** The updater replaces only `Relay.app/Contents/MacOS/relay`, so a release ships the bundle once, for first install, and every update after that is the bare binary, as on the other platforms. The bundle is ad-hoc signed, and its `Info.plist` and icon (`build/icon.icns`, committed so it never changes) are identical in every release. CI takes the update binary from inside that release's signed bundle, so it keeps any installed bundle's signature valid.

## Trust

Every release is signed with an Ed25519 private key that exists only on the release machine and in the `UPDATER_SIGNING_KEY` CI secret. The matching public key is compiled into the app. The update server and the file storage never see the private key. So if the server, the storage or the network is compromised, an attacker can stop updates from arriving but cannot make a client install anything of their own.

The release workflow compiles each platform on a matching runner with `release compile`, which needs only the public key, then signs everything in one final job with `release sign`. The private key is exposed to exactly one job.

## Threat model and limits

- The local demo uses plain HTTP. The deployed server uses HTTPS. Signatures protect integrity either way.
- A compromised server can withhold updates or keep serving an old release. It cannot forge one. Preventing an indefinite freeze would need signed, expiring manifests as in TUF, noted as future work.
- The private key is a single point of trust. Rotation would ship a list of trusted public keys in the client and sign new releases with the new key while old clients still accept the old one.
- Downloads are size capped, every network call has a timeout, and the server serves nothing outside `releases/<version>/<file>`.

## Reliability in detail

- **Interrupted download.** It goes to a temp file next to the executable and is deleted on any mismatch. Leftovers are cleaned on the next start.
- **Crash during the swap.** The last step is a single atomic rename, so the executable path never holds a half written program. On Windows there is a brief window between two renames, and a failed second rename puts the old program back.
- **A version that passes its self-test but keeps crashing.** After two failed starts the third start rolls back to `.old` and records the version so it is never installed again on that machine. After 30 seconds of healthy running (`--confirm-after`) the update is confirmed and `.old` is deleted. A newer update waits until the current one is confirmed.
- **Two copies updating at once.** A lock file prevents it. A lock older than ten minutes is treated as left by a crash.
- **A corrupt state file.** It is moved aside and treated as empty. State writes are atomic.
- **A release that fails its checks.** If a download does not match the manifest's size or hash, or the new binary fails its self-test, Relay tries it once and retries twice, then stops trying that exact release (version and hash) and records "gave up" in the history. This lasts only until the app restarts or a different release is offered, and is never saved. The size and the download are not covered by the signature, so anyone able to tamper with them could otherwise make a genuine release be skipped forever. Only a crash-loop rollback skips a version permanently.
- **Server unreachable or a bad response.** Logged and retried at the next check, with no limit, because the release is not at fault. The program never exits because of the updater.
- **Restart.** On macOS and Linux the program `exec`s the new binary, keeping its process ID. On Windows a running executable can be renamed but not overwritten, so the old one is renamed aside, the new one starts as a new process, and the old one exits.

## Operator page

With `ADMIN_TOKEN` set (16 characters or more), the server serves `/admin` behind HTTP basic auth, with any username and the token as the password. It shows each channel's version, rollout percentage and pause switch, the releases on disk, and the clients that checked in recently with the version they run. It can also import a release that CI published to GitHub, given `GITHUB_REPO` and, for a private repository, `GITHUB_TOKEN`. Everything it does, `release promote` does from the command line.

## Hosting

The server is a stateless binary that reads a directory, so it runs unchanged on Cloud Run, Fly.io or any container host (`Dockerfile`). Set `BASE_URL` to its public address. On Cloud Run the `releases` directory is a Cloud Storage bucket mounted as a volume, so promotions and imports persist across restarts.

```sh
gcloud run deploy relay-updateserver --source . --region us-east1 --allow-unauthenticated \
  --max-instances 1 --env-vars-file run-env.yaml \
  --add-volume name=releases,type=cloud-storage,bucket=<bucket> \
  --add-volume-mount volume=releases,mount-path=/data
```

`run-env.yaml` holds `BASE_URL`, `RELEASES_DIR: /data`, `ADMIN_TOKEN` and `GITHUB_REPO`. After the first deploy, `.github/workflows/deploy.yml` redeploys on every push to `main` that touches the server. It signs in to Google with Workload Identity Federation, set up once by `scripts/setup-deploy-identity.sh`, so no cloud key is stored in GitHub. The operator page's client list is kept in memory per instance, which is why the service runs one instance.

Release builds get their updates from this server, compiled in through `release compile --cloud-server <url>`. A local build has no cloud server and uses one on the same computer.

## Shipping a release by hand

```sh
go run ./cmd/release keygen                                  # once; commit pubkey.txt, never signing.key
go run ./cmd/release build --version 1.2.0                   # releases/1.2.0/{app-*, release.json}
go run ./cmd/release build --version 1.2.0 --desktop --targets darwin/arm64
go run ./cmd/release promote --version 1.2.0 --channel beta --percent 10
go run ./cmd/release promote --version 1.2.0                 # stable, 100 percent
go run ./cmd/release promote --version 1.2.0 --paused        # kill switch
```

Building and promoting are separate on purpose. A build can sit in `releases/` for as long as needed before any client hears about it.

## Dependencies

The standard library, plus `golang.org/x/mod/semver` for version comparison and Wails for the desktop window. Only the `desktop` build tag pulls in Wails. The updater, server and release tool use neither.

