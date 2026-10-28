# Relay

Relay is a small desktop app that keeps itself up to date. Every minute it asks an update server whether a newer version exists. When one does, it downloads it, checks that it was really published by us, makes sure the new version starts, swaps it in and restarts. Nobody has to touch the machine. If a new version keeps crashing, Relay puts the old one back.

The window shows the running version, a history of every update, and Bolt, a small robot who reacts to what the updater is doing.

This repository holds all three parts of the system.

| Part | What it does |
|---|---|
| The app (`cmd/app`) | Runs on Mac, Windows and Linux and updates itself |
| The update server (`cmd/updateserver`) | Tells each app which version it should run and serves the files |
| The release tool (`cmd/release`) | Builds and signs a release, then makes it available to apps |

How it works, and why it is safe, is in [docs/DESIGN.md](docs/DESIGN.md).

## Download and run the app

Download the file for your computer from the [latest release](https://github.com/hacka-tron/Relay/releases/latest).

| Computer | File |
|---|---|
| Mac with Apple Silicon | `Relay-macos-arm64.zip` |
| Mac with Intel | `Relay-macos-x86_64.zip` |
| Windows | `app-windows-amd64.exe` |
| Linux | `app-linux-amd64` or `app-linux-arm64` |

The apps are not signed with a paid Apple or Microsoft certificate, so each computer warns you once the first time you open one. Updates after that install on their own.

**Mac.** Unzip the file and drag `Relay.app` into Applications. Open it from there, not from Downloads, because macOS opens apps in Downloads from a read-only copy that cannot update itself. If macOS blocks it, go to System Settings, then Privacy and Security, and click Open Anyway. You can also install it from Terminal, which skips the warning.

```sh
gh release download --repo hacka-tron/Relay --pattern Relay-macos-arm64.zip
ditto -x -k Relay-macos-arm64.zip /Applications && open /Applications/Relay.app
```

**Windows.** Put `app-windows-amd64.exe` in a folder you own, for example a new `Relay` folder on your Desktop, and double-click it. If Windows warns you, click More info, then Run anyway.

**Linux.** The Linux app shows its window in your browser.

```sh
chmod +x app-linux-amd64 && ./app-linux-amd64
```

### Settings

Click the gear at the bottom left of the window.

- **Get updates from.** Cloud, which is our live server and the default, or This computer, which is an update server on port 8080 of your own machine.
- **Check every.** From 10 seconds to 1 hour. The default is 1 minute.

Your choices are saved on your computer and apply straight away.

## Run an update server on your computer

You need [Go](https://go.dev/dl/) 1.26 or later and a copy of this repository.

```sh
git clone https://github.com/hacka-tron/Relay.git && cd Relay
go run ./cmd/updateserver
```

The server listens on port 8080 and serves whatever is in the `releases` folder. Then open the app's settings and choose This computer.

To see the whole system work end to end in about a minute, run the demo instead. It builds two versions, starts a server and an app, and shows the app updating itself and then refusing a tampered build.

```sh
make demo                 # Mac or Linux
.\scripts\demo.ps1        # Windows
```

## Test a new version

**1. Install the current version** of the app as described above, and leave it running.

**2. Publish a new version** by tagging a commit and pushing the tag. GitHub Actions builds the app for every platform, signs it and publishes a release. This takes about five minutes.

```sh
git tag -a v1.6.0 -m "v1.6.0"
git push origin v1.6.0
```

**3. Offer it to your app.** Pick the server your app is set to.

If your app uses This computer, download the release into the local server and offer it.

```sh
make fetch VERSION=1.6.0
go run ./cmd/release promote --version 1.6.0
```

On Windows without `make`, replace the first line with `gh release download v1.6.0 --dir releases/1.6.0`.

If your app uses Cloud, upload the release to the live server's storage, then offer it on the admin page at `https://relay-updateserver-107741567910.us-east1.run.app/admin` with any username and the admin token as the password.

```sh
make fetch VERSION=1.6.0
gcloud storage cp -r releases/1.6.0 gs://relay-513017-releases/
```

**4. Watch it update.** Within one check interval the app finds the new version and Bolt hops while it installs. The version at the top right changes, and the history lists each step, ending with "confirmed healthy" about 30 seconds later.

| Computer | What you see |
|---|---|
| Mac | The window closes and reopens straight away as the new version |
| Windows | The window closes and a new one opens a second later |
| Linux | The browser page shows the new version on its own |

To check the version without the window, run the app with `--version`. On a Mac that is `/Applications/Relay.app/Contents/MacOS/relay --version`.

## Development

```sh
make check    # format, vet and build
make test     # run all tests
```
