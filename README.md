# 🛰️ Servo

**A dashboard for managing a dedicated game server, sized for small friend groups.**

One host, one game server, one always-on HTTPS dashboard: start/stop/restart, updates, scheduled
nightly restarts, backups with one-click restore, player counts, and in-game restart warnings.
Built on [Sprout](https://github.com/Data-Corruption/sprout), so install, self-update, TLS,
auth, and the daemon lifecycle are already battle-tested plumbing.

## How it works

Servo itself knows nothing about any particular game. All game-specific logic lives in a
**driver**: a single shell script implementing a small verb contract (`start`, `stop`, `status`,
`backup`, ...). Servo orchestrates; the driver executes.

```mermaid
graph LR
    Browser[Web UI] <-->|HTTPS + polling| Daemon[servo daemon]
    Daemon -->|"verbs (start, stop, backup, ...)"| Driver[driver script]
    Driver -->|podman / steamcmd / whatever| Game[game server]
```

- **Drivers are installed over SSH** (`~/.servo/drivers/`), never through the UI — a driver is
  arbitrary code, so getting one onto the box requires shell access. Activation (with validation)
  happens in the dashboard, admin only. See [docs/DRIVERS.md](docs/DRIVERS.md) to write one;
  [`drivers/fedora-palworld.sh`](drivers/fedora-palworld.sh) ships as the reference driver
  (Palworld via rootless podman).
- **One operation at a time.** Long ops (install, update, backup, restore) run async; the
  dashboard's activity panel shows what's happening, and admins see the live driver output.
- **Daily restart window** (optional): warns players in-game N minutes ahead (if the driver
  supports `notify`), takes a backup first (if enabled), and never starts a server someone
  stopped on purpose.
- **Backups** are single compressed archives produced by the driver, retention-pruned, and
  downloadable/restorable from the dashboard. Restore ships in v1 because a backup you've never
  restored is a hope, not a backup.
- **Permissions** are a bitmask split into `game.*` (control / backup / restore) and `servo.*`
  (settings / daemon control), plus `admin` for driver activation and background image uploads.
  Credentials are created via `servo password add --label alice --perms "game.control game.backup"`.

## Getting started

1. Install Servo on the host (see [docs/INSTALLATION.md](docs/INSTALLATION.md)).
2. Create a login: `servo password add --label admin`.
3. Drop a driver in over SSH and make it executable:
   ```sh
   scp drivers/fedora-palworld.sh host:~/.servo/drivers/
   ssh host chmod +x '~/.servo/drivers/fedora-palworld.sh'
   ```
   (For the Palworld driver: edit the config block — passwords! — before copying.)
4. Open the dashboard (`https://host:8484`), go to settings, activate the driver, press
   **Install**, then **Start**.
5. Optional: set the nightly restart time, enable backups, upload a background image, and hand
   out scoped credentials to the group.

## Design & internals

- [docs/DESIGN.md](docs/DESIGN.md) — the design doc: driver contract, job model, scheduler,
  security model.
- [docs/DRIVERS.md](docs/DRIVERS.md) — driver authoring guide.
- [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) — the underlying Sprout template architecture
  (DB, auth, self-update, release pipeline).
- [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) — building and releasing.

## Platform support

Linux (`amd64`/`arm64`) with `systemd --user`, same as Sprout. The reference driver targets
Fedora + rootless podman, but drivers can wrap anything the host can run.

<br>

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE.md)
