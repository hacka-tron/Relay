# Relay

Relay is a small desktop app that keeps itself up to date. Every minute it asks an update server whether a newer version exists. When one does, it downloads it, checks that it was really published by us, makes sure it starts, swaps it in and restarts. Nobody has to touch the machine, and if a new version keeps crashing, Relay puts the old one back.

Its window shows the running version, the history of every update, and Bolt, a small robot who reacts to what the updater is doing.

How it works, and why it is safe, is in [docs/DESIGN.md](docs/DESIGN.md).

## Try it on your computer

The demo runs everything locally, with its own throwaway signing key. It works the same on Mac, Windows and Linux. All you need is [Go](https://go.dev/dl/) 1.26 or later.

**1. Start Relay and a local update server.**

```sh
git clone https://github.com/hacka-tron/Relay.git
cd Relay
go run ./cmd/demo start
```

Relay 1.0.0 opens in your browser. Leave this terminal running.

**2. Publish a new version.** In a second terminal, in the same folder, run

```sh
go run ./cmd/demo next
```

Within a few seconds Relay finds the new version, installs it and restarts. Bolt hops while it installs, the version at the top changes, and both the window and the terminal list each step. Run it again to publish the version after that.

**3. Publish a tampered version.**

```sh
go run ./cmd/demo bad
```

This builds the next version, signs it, then changes its bytes the way an attacker would. Relay downloads it, sees that it no longer matches the hash it was signed with, and refuses it. It tries twice more, then stops trying that release and keeps running. Run `next` afterwards and Relay installs the newer good version.

**4. Stop.** Press Ctrl-C in the first terminal. Everything the demo made is in the `demo` folder.

On Mac and Linux, `make demo`, `make next` and `make bad` do the same.

## Install the app

Download the file for your computer from the [latest release](https://github.com/hacka-tron/Relay/releases/latest). The apps are not signed with a paid Apple or Microsoft certificate, so your computer warns you once, the first time you open one.

### Mac

1. Download `Relay-macos-arm64.zip` for Apple Silicon, or `Relay-macos-x86_64.zip` for Intel.
2. Unzip it and drag `Relay.app` into Applications. Run it from there, because a Mac cannot update apps that run from Downloads.
3. Open it. If macOS blocks it, go to System Settings, then Privacy and Security, and click Open Anyway.

### Windows

1. Download `app-windows-amd64.exe`.
2. Move it into a folder you own, for example a new folder called Relay on your Desktop.
3. Double-click it. If Windows warns you, click More info, then Run anyway.

### Linux

1. Download `app-linux-amd64`, or `app-linux-arm64` for ARM.
2. Make it executable and run it. Its window opens in your browser.

```sh
chmod +x app-linux-amd64
./app-linux-amd64
```

### How often it checks

Relay checks for updates every minute. To change that, click "Checks every 1 min" at the bottom left of the window and pick from 10 seconds to 1 hour. Your choice is saved on your computer.

## Publishing a release

This part needs write access to this repository, because releases are signed with a key that only its GitHub Actions can use.

1. Tag a commit and push the tag. GitHub Actions builds the app for every platform, signs it and publishes a release in about five minutes.

    ```sh
    git tag -a v1.6.0 -m "v1.6.0"
    git push origin v1.6.0
    ```

2. Upload it to the live server's storage.

    ```sh
    make fetch VERSION=1.6.0
    gcloud storage cp -r releases/1.6.0 gs://relay-513017-releases/
    ```

3. Offer it to apps on the admin page at `https://relay-updateserver-107741567910.us-east1.run.app/admin`, signing in with any username and the admin token as the password. Every installed copy updates within a minute.

## Development

```sh
make check    # format, vet and build
make test     # run all tests
```
