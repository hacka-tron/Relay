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

1. **What kind of program?** A long-running background program (agent or daemon). Updates are silent and applied automatically. It runs under a supervisor that restarts it if it exits.
2. **Who are the clients?** End-user machines we do not control, possibly offline for long periods.
3. **Silent or prompted?** Silent for this program. A prompt-to-restart mode for interactive apps and operator-controlled pinning for enterprise are natural extensions.
4. **Code signing for Gatekeeper and SmartScreen?** Out of scope (needs paid certificates). Our own Ed25519 signatures cover update integrity.
5. **Package managers (brew, apt, winget)?** Out of scope. If installed by a package manager, the app should defer to it.
6. **Rollback?** Yes, assumed required.
7. **Server hosting?** Runs locally for the demo. Production: server on Cloud Run, Fly.io or similar, binaries on S3 plus CloudFront or GitHub Releases.

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
├── scripts/            # demo.sh and demo.ps1
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
12. **Flags**: `--server`, `--channel`, `--interval`, `--confirm-after`, `--selftest`, `--version`.

## Release tool

- `release keygen` writes `signing.key` (private, gitignored) and `pubkey.txt`.
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
