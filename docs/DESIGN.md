# Servo — Design Doc

Living design doc — the reference for how Servo works and why. Settled decisions and the icebox
are at the bottom.

Servo is a dashboard for managing a dedicated game server for a small friend group. Built on
[Sprout](https://github.com/Data-Corruption/sprout), so the daemon/CLI lifecycle, LMDB config,
HTTPS dashboard, session auth, perms, and self-update plumbing are already solved. This doc only
covers what Servo adds on top.

## Goals

- Manage **one game server per install** through a generic driver interface: install, start, stop,
  restart, update, status, backup, restore, player notification, player count, version reporting.
- Drivers are **external executables** implementing a small verb contract, so supporting a new game
  (or a new host distro) never requires rebuilding Servo.
- Scheduled daily restart window, with optional backups performed during it.
- Small quality-of-life extras: custom background images for login/dashboard, soft warning when a
  driver is stale relative to the live server/container version.

## Non-Goals

- Multi-server / multi-driver-at-once management. One active driver, one game server. Run two
  installs if you need two servers.
- Being a Pterodactyl/AMP competitor. No user-facing server provisioning, no mod managers, no
  per-player billing nonsense. Friend-group scale, single host.
- Driver marketplace / in-UI driver installation. Getting a driver onto the box is deliberately an
  SSH operation (see Security).

## Drivers

### Shape

A driver is a single executable file (POSIX sh expected in practice, but anything executable
works) living in the drivers dir. Servo invokes it as:

```
<driver> <verb> [args...]
```

and communicates via argv, environment variables, exit codes, and stdout. This is the LSB
init-script / Nagios-plugin pattern: language-agnostic, trivially testable by hand
(`./fedora-palworld.sh status; echo $?`), and it keeps Servo a dumb orchestrator with no
game-specific knowledge compiled in.

A `driver.template.sh` ships in the repo with every verb stubbed out and comments explaining the
contract. Writing a driver = copy, fill in the blanks.

### Verb contract (Driver API v1)

| Verb                | Required | Behavior | Stdout |
| ------------------- | :------: | -------- | ------ |
| `describe`          | yes | Print driver metadata. Must be fast, no side effects. | `KEY=VALUE` lines (below) |
| `deps`              | yes | Verify every tool the driver needs is present (`command -v ...`). Fast, no side effects. | names of missing tools, one per line |
| `status`            | yes | Report whether the game server is online. | optional human-readable detail |
| `start`             | yes | Start the server. Idempotent (already running = success). | log output |
| `stop`              | yes | Stop the server gracefully. Idempotent. | log output |
| `install`           | yes | First-time setup: pull image, create dirs/volumes, etc. | log output |
| `update`            | yes | Update the server/image. **Convention: check the current version first and succeed as a no-op if already up to date** — pressing the button when current must be harmless. Caller guarantees server is stopped. | log output |
| `backup`            | yes | Archive server data into `$SERVO_BACKUP_DIR` as a **single compressed file** (format is the driver's choice, extension conveys it — `.tar.gz` in practice). Caller guarantees server is stopped. | absolute path of created archive (last line) |
| `restore <archive>` | no  | Restore server data from the given archive (one previously produced by this driver's `backup`). Caller guarantees server is stopped. | log output |
| `uninstall`         | no  | Full teardown: remove everything `install`/`update` created outside `$SERVO_DATA_DIR` (containers, images, units...). Caller guarantees server is stopped, and deletes `$SERVO_DATA_DIR` itself afterwards. Backups are kept. | log output |
| `notify <message>`  | no  | Deliver a message to in-game players (RCON, etc.). | log output |
| `players`           | no  | List currently connected players. Fast; polled alongside `status`. | one player name per line (count = line count) |
| `version`           | no  | Print live game server version. | version string |
| `container-version` | no  | Print live container image version/tag. Only meaningful if `CONTAINERIZED=true`. | version string |

Exit codes:

- `0` — success. For `status`: server is online. For `deps`: all dependencies present.
- `3` — for `status` only: server is stopped (LSB convention). Not an error.
- `4` — verb not supported by this driver (how optional verbs decline).
- anything else — failure. Servo surfaces stderr/stdout in the UI and log. For `deps`: one or
  more dependencies missing, stdout lists them.

`restart` is not a verb; Servo composes `stop` then `start`. Same for the backup and restore
dances: `stop` → `backup` → `start` and `stop` → `restore <archive>` → `start` are orchestrated by
Servo so the behavior is uniform across drivers — the driver only has to produce or consume an
archive. Compression is the driver's job too: it knows its data, and Servo never has to hold an
uncompressed copy or re-pack anything at download time.

`backup` and `restore` are deliberately symmetric from day one. Restore ships in v1 (contract
*and* UI) — a backup you've never restored is a hope, not a backup, and deferring the verb would
let driver authors design archive formats with no restore path.

`describe` output:

```
DRIVER_API=1
NAME=Palworld (Podman, Fedora)
GAME=palworld
CONTAINERIZED=true
TARGET_SERVER_VERSION=v0.6.4      # optional; version the driver was written against
TARGET_CONTAINER_VERSION=v1.2.3   # optional; image version the driver was written against
```

Unknown keys are ignored (forward compat). `DRIVER_API` lets Servo refuse to activate a driver
written for a future contract version.

Environment provided to every invocation:

- `SERVO_BACKUP_DIR` — where `backup` must write archives. Exclusive to this driver: a
  subdirectory of `~/.servo/backups/` named after the driver file, created by Servo.
- `SERVO_DATA_DIR` — scratch/persistent dir exclusive to this driver: a subdirectory of
  `~/.servo/driver-data/` named after the driver file, created by Servo and deleted by Servo
  after a successful `uninstall`.
- `SERVO_VERSION` — Servo's version, for drivers that care.

Keying both dirs by driver filename means drivers don't see each other's state, backup retention
is naturally per-driver, and restore only offers archives the active driver produced.
The one caveat: renaming a driver file orphans its dirs (the operator moved it, the operator can
move them too).

Timeouts (Servo kills the process group on expiry): `describe`/`deps`/`status`/`notify`/`players`/
`version`s get seconds, `start`/`stop` get minutes, `install`/`update`/`backup`/`restore`/
`uninstall` get a generous cap (configurable later if it bites).

### Installation & activation

- Drivers are placed in `~/.servo/drivers/` **manually over SSH** (scp/curl + chmod +x). There is
  intentionally no upload-a-driver UI: a driver is arbitrary code running as the service user, so
  writing that dir must require shell access, which is a strictly stronger credential than a
  dashboard password.
- The UI (admin perm) lists executables found in the drivers dir and lets you **activate exactly
  one**. Activation runs `describe` (validates `DRIVER_API`) and then `deps`; if `deps` fails,
  activation is refused and the missing tools are shown. That turns "backup silently failed at
  4am because tar wasn't installed" into a clear error at activation time. Selection is from the
  enumerated dir listing only — never a client-supplied path.
- Switching drivers is guarded: activation is refused while an operation is running or while the
  current driver's server is online — one game server at a time, and stopping it is an explicit
  operator action. (A failed status probe on the outgoing driver doesn't block the switch; a
  broken driver may be exactly why the operator is switching.) Beyond the guard, switching
  doesn't touch the old server's resources — install/uninstall remain explicit buttons.

### Staleness warning

On activation and periodically (piggybacking on the status poll), Servo compares
`TARGET_SERVER_VERSION` / `TARGET_CONTAINER_VERSION` from `describe` against live `version` /
`container-version` output. Plain string inequality → small warning badge in the UI ("driver was
written for v1.2.3, server is at v1.3.0"). Purely informational, never a gate. No semver parsing —
a soft badge doesn't justify it, and game version strings are chaos anyway.

## Operations & job model

Everything the driver does is an **operation**, and only one runs at a time — a mutex in the
daemon, not a queue. `install`, `update`, `backup`, and `restore` can run for minutes, so
operations are async:

- POST returns `202` immediately; the op runs in a goroutine.
- Servo captures combined stdout/stderr into an in-memory ring buffer (capped, tens of KB — old
  output scrolls off, nobody needs megabytes of image-pull progress bars).
- Control buttons disable while an op is running.
- Last operation result (verb, exit code, timestamp, tail) is persisted to LMDB so it survives a
  daemon restart; the live log buffer doesn't need to.

### Activity panel

How long-running ops surface in the UI: a panel on the dashboard that shows the current operation
("Backing up…", verb + elapsed time) with a small animation while active, and the last operation's
result when idle. **Admins get the live driver output streamed into the panel instead** of just
the label.

Transport is plain polling — the browser hits a status endpoint every second or two while an op
is running (backing off when idle), same pattern the template already uses for restart-status.
The response carries op state plus, for admins, the log tail; the client sends the byte offset it
has and gets only new bytes back. At friend-group user counts, polling an in-memory buffer is
effectively free — SSE/WebSockets would buy nothing but connection-lifecycle code. If polling
ever feels laggy (it won't at this scale), SSE is the drop-in upgrade path.

The scheduler (below) acquires the same op mutex, so a scheduled restart can't race a button
press, and scheduler-initiated ops show up in the activity panel like any other.

## Scheduler

Config: daily restart time as `"HH:MM"` (host-local time) + enabled flag, and a backups-enabled
flag. No cron syntax, no dependency — the daemon computes the next occurrence and sleeps
(recomputed on config change and after each run).

At the window:

| Server state | Backups off | Backups on |
| ------------ | ----------- | ---------- |
| online       | stop → start | stop → backup → start |
| offline      | nothing | backup only (safe while stopped; do **not** start a server someone stopped on purpose) |

If the active driver supports `notify` and the server is online, the scheduler warns players
before the window ("server restarting in 5 minutes"). Lead time in minutes is a config field
(default 5, `0` = no warning).

The scheduled window never runs `update`. Unattended game updates are how shit gets fucked —
updating stays a deliberate button press.

## Backups

- Produced by the driver as single compressed archives (see verb contract), named by the driver,
  living in the driver's own subdirectory of `~/.servo/backups/`.
- Retention: keep the newest N archives (config, default ~5), pruned by Servo after each
  successful backup.
- **Download over the UI**: the backups list on the dashboard offers each archive as a download —
  Servo just streams the file with a `Content-Disposition` header, no re-compression, no temp
  copies. Gated by `game.backup`.
- **Restore over the UI**: each listed backup gets a restore button behind a confirm modal.
  Gated by `game.restore` (its own bit — see Permissions) since it's the one destructive action
  in the app. Runs stop → `restore` → start via the job model. Hidden if the active driver
  declines the verb (exit 4).
- "Backup now" button runs the same stop → backup → start sequence as the scheduler, on demand.
- Off-host sync (rclone etc.) is out of scope for v1 — the operator's problem, or a future
  driver concern.

## Storage layout

Additions to the standard Sprout home dir:

```
~/.servo/
  db/ logs/ secrets/ tmp/     # from template
  drivers/                    # driver executables, installed via SSH
  driver-data/<driver>/       # per-driver $SERVO_DATA_DIR
  backups/<driver>/           # per-driver $SERVO_BACKUP_DIR, retention-pruned
  backgrounds/                # uploaded login/dashboard background images
```

## Permissions

The bitmask in `internal/types/perms.go` is rebuilt around two namespaces: `game.*` protects the
game server (driver operations), `servo.*` protects the dashboard/daemon itself. This replaces the
template's `settings`/`server.control` starter bits — `server.control` in particular was about to
mean two different things (daemon restart vs game restart).

- `game.control` — start, stop, restart, update, notify.
- `game.backup` — backup-now + downloading archives.
- `game.restore` — restore. Its own bit because it's the one destructive action; grant it per
  credential as trust allows.
- `servo.settings` — restart schedule, notify lead time, backup toggle/retention, binds, log
  level.
- `servo.control` — daemon stop/restart/self-update (what the template's `server.control`
  actually gated).
- `admin` — driver activation, uninstall, background image upload, all bits.

Deliberately not one bit per verb: nobody grants `start` without `stop`, and granularity you'll
never exercise is just noise when adding credentials. Bits group by risk tier; splitting one out
later (e.g. `game.update`) is cheap if it ever matters.

## UI

Two app pages plus the existing settings/login:

- **Dashboard** (`/`): server online/offline, player count/roster when the driver supports
  `players`, big start/stop/restart buttons, update + backup-now, the activity panel (see job
  model), versions (driver target vs live) with the staleness badge, backups list with download
  and restore actions.
- **Settings**: existing template settings + restart time, notify lead time, backups toggle,
  retention count, driver activation (list + activate), background image upload.

Background images: admin uploads via the settings page (size-capped, content-type sniffed, stored
in `backgrounds/`, filename regenerated server-side). Login page background is served on the
auth-exempt asset path since it renders pre-login.

## First driver: `fedora-palworld.sh`

Drives [thijsvanloef/palworld-server-docker](https://github.com/thijsvanloef/palworld-server-docker)
on Fedora via **podman** (ships with Fedora, no extra repo, the image runs fine under it).

- `deps` — `command -v` checks for podman, tar, gzip.
- `install` — pull the image, create the data dir, create (not start) the container with the
  standard mounts/env.
- `start`/`stop` — `podman start` / `podman stop` (generous stop timeout; Palworld saves on
  shutdown).
- `status` — inspect container running state; map to exit 0/3.
- `update` — pull the newer image, recreate the container (server already stopped by Servo).
- `backup` — tar.gz the mounted save data dir into `$SERVO_BACKUP_DIR`.
- `restore` — wipe the save data dir and untar the given archive over it (server already stopped).
- `uninstall` — remove the container and image; Servo deletes the data dir (server already
  stopped).
- `notify` — `podman exec` the image's bundled rcon-cli to broadcast the message in-game.
- `players` — rcon-cli `ShowPlayers` (returns CSV of name,playeruid,steamid); print the name
  column, one per line.
- `container-version` — image tag or `org.opencontainers.image.version` label.
- `version` — Palworld doesn't expose its version cleanly; exit 4 (unsupported) for now, maybe
  RCON later.

One podman-specific wrinkle: rootless podman containers don't auto-start on boot, and the
`podman generate systemd` / Quadlet story is its own rabbit hole. For v1 the driver just manages
the container directly and Servo's own daemon (already a systemd user service) is what survives
reboots — whether the game server should auto-start after host reboot is left to the driver
author.

## Security notes

- Drivers are arbitrary code executed as the service user. Mitigation is procedural, not
  technical: install requires SSH, activation requires admin, and selection is from the dir
  listing only. Document loudly; don't pretend to sandbox.
- Driver stdout/stderr is rendered in the UI — escape it as text (a compromised game server's
  output flows through here).
- Uploaded backgrounds: cap size, verify image content type, regenerate filenames. Never serve
  user-controlled filenames.
- Backup download/restore endpoints select archives from the enumerated backups dir listing only
  (same rule as driver activation) — never a client-supplied path.

## Decided

- **Name**: Servo.
- **Container runtime for the first driver**: podman (Fedora-native).
- **No scheduled/automatic `update`** — always a deliberate button press, never a cron job.
- **`notify` and `restore` are optional verbs in Driver API v1**, not deferred to v2. Notify lead
  time is configurable (minutes, 0 disables).
- **Drivers compress backups** (single archive file); Servo streams them as-is for UI download.
- **Perms split into `game.*` / `servo.*` namespaces**, grouped by risk tier rather than one bit
  per verb. Restore gets its own bit.
- **`update` checks before updating** (convention): drivers verify the current version and no-op
  successfully if already up to date.
- **`players` is an optional verb in v1** — the RCON complexity lives in the driver, Servo just
  counts lines.
- **Long ops surface via an activity panel** (verb + animation + elapsed for everyone, live
  output for admins) fed by simple polling. No SSE/WebSockets at this scale.

## Icebox

- **Update-available indicator.** A future optional verb (`check-update`?) could power a badge
  without auto-applying anything.
