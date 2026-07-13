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
- **Permissions** are a bitmask split into `game.*` (the game server) and `servo.*` (the
  dashboard/daemon itself), plus `admin`. See [Permissions](#permissions) below.

## Permissions

Every credential carries a set of permission bits. The UI only shows what the credential can
actually use, and the API enforces the same bits server-side.

**Every logged-in user** can see the dashboard: game server status, player count/list, the join
info copy buttons (address/password), the activity panel, and a personal dark-mode toggle in
settings (a local preference, not a server setting).

| Permission | Grants |
| --- | --- |
| `game.control` | Start, stop, restart, and update the game server |
| `game.backup` | Backup now; see and download the backups list |
| `game.restore` | Restore a backup — the one destructive action, so it's its own bit |
| `servo.settings` | The settings page: restart schedule, backups toggle/retention, player warning lead, connection info, global theme override, log level, binds |
| `servo.control` | Stop, restart, and self-update the Servo daemon (also sees the update-available notice) |
| `admin` | All of the above, plus driver activation, background images (upload/clear, blur, alignment), and live driver output in the activity panel |

Create credentials with a space-separated perms spec; a leading `!` clears a bit:

```sh
servo password add --label alice --perms "game.control game.backup"
servo password add --label bob --perms "admin !game.restore"   # everything except restore
```

## Getting started

Servo runs on Linux (`amd64`/`arm64`) with `systemd --user`. The reference driver targets
Fedora + rootless podman, but drivers can wrap anything the host can run.

1. Install Servo on the host (see [docs/INSTALLATION.md](docs/INSTALLATION.md)).
2. Create a login: `servo password add --label admin`.
3. Drop a driver in over SSH and make it executable:
   ```sh
   scp drivers/fedora-palworld.sh host:~/.servo/drivers/
   ssh host chmod +x '~/.servo/drivers/fedora-palworld.sh'
   ```
   (For the Palworld driver: edit the config block — passwords! — before copying.)
4. Open the dashboard (`https://host:8829`), go to settings, activate the driver, press
   **Install**, then **Start**.
5. Optional: set the nightly restart time, enable backups, upload a background image, and hand
   out scoped credentials to the group.

## Going deeper

- **Running a server?** [docs/INSTALLATION.md](docs/INSTALLATION.md) covers install, first
  login, and reverse-proxy setup.
- **Writing a driver?** [docs/DRIVERS.md](docs/DRIVERS.md) is the authoring guide;
  [`drivers/driver.template.sh`](drivers/driver.template.sh) is the starting point.
- **Tinkering on Servo?** [CONTRIBUTING.md](CONTRIBUTING.md) is the dev quickstart;
  [docs/DESIGN.md](docs/DESIGN.md) has the design rationale (driver contract, job model,
  scheduler, security model).

<br>

[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE.md)
