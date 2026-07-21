# Writing Servo Drivers

A driver is a single executable file (POSIX sh in practice, but anything executable works) that
teaches Servo how to manage one game server. Servo invokes it as:

```
<driver> <verb> [args...]
```

and communicates via argv, environment variables, exit codes, and stdout. Servo has no
game-specific knowledge compiled in — the driver is the whole integration.

**Quick start**: copy [`drivers/driver.template.sh`](../drivers/driver.template.sh), fill in the
verbs, install it over SSH:

```sh
scp my-game.sh host:~/.servo/drivers/
ssh host chmod +x '~/.servo/drivers/my-game.sh'
```

then activate it from the Servo settings page (admin). For a complete real-world example, see
[`drivers/fedora-palworld.sh`](../drivers/fedora-palworld.sh).

> [!IMPORTANT]
> Drivers are arbitrary code running as the Servo service user. That is why installing one
> requires SSH — there is deliberately no upload-a-driver UI, and there never will be. Only
> install drivers you've read.

## Environment

Every invocation receives:

| Variable | Meaning |
| --- | --- |
| `SERVO_BACKUP_DIR` | where `backup` must write archives — exclusive to this driver |
| `SERVO_DATA_DIR` | scratch/persistent dir exclusive to this driver |
| `SERVO_VERSION` | Servo's version string |

Both dirs are subdirectories (of `~/.servo/backups/` and `~/.servo/driver-data/`) named after the
driver file, created by Servo before any verb runs. No other driver ever sees them, so use
`$SERVO_DATA_DIR` directly — no need to carve out your own subdir. Two caveats: renaming a driver
file orphans its dirs, and `$SERVO_DATA_DIR` is deleted by Servo after a successful `uninstall`.

## Exit codes

- `0` — success. For `status`: server online. For `deps`: all dependencies present.
- `3` — `status` only: server is stopped (LSB convention). Not an error.
- `4` — verb not supported by this driver (how optional verbs decline).
- anything else — failure. stdout/stderr is surfaced in the dashboard log.

## Verbs (Driver API v1)

| Verb | Required | Behavior | Stdout |
| --- | :---: | --- | --- |
| `describe` | yes | Print metadata (below). Fast, no side effects. | `KEY=VALUE` lines |
| `deps` | yes | Verify every external tool the driver uses is installed. Fast, no side effects. Run at activation — a failure blocks activation. | missing tool names, one per line |
| `status` | yes | Is the server online? | optional human-readable detail |
| `start` | yes | Start the server. Idempotent: already running = success. | log output |
| `stop` | yes | Stop gracefully. Idempotent: already stopped = success. | log output |
| `install` | yes | First-time setup: pull image, create dirs, etc. | log output |
| `update` | yes | Update the server/image. **Convention: check the current version first and succeed as a no-op if already current.** Server is stopped when called. | log output |
| `backup` | yes | Write ONE compressed archive into `$SERVO_BACKUP_DIR` (format is yours; extension conveys it). Server is stopped when called. | absolute archive path as the LAST line |
| `restore <archive>` | no | Restore from an archive previously produced by this driver's `backup`. Server is stopped when called. | log output |
| `uninstall` | no | Full teardown: remove everything `install`/`update` created **outside** `$SERVO_DATA_DIR` (containers, images, units...). Server is stopped when called; Servo deletes `$SERVO_DATA_DIR` itself afterwards. Backups are kept. | log output |
| `notify <message>` | no | Deliver a message to in-game players (RCON etc.). Used by the scheduler to warn before restart windows. | log output |
| `players` | no | List connected players. Polled alongside `status`. | one player name per line |
| `metrics` | no | Compact live server metrics. Polled alongside `status`; formatting belongs to the driver. | one short human-readable line |
| `version` | no | Live game server version. | version string |
| `container-version` | no | Live container image version/tag. | version string |

There is no `restart` verb — Servo composes `stop` then `start`. Likewise `update`, `backup`, and
`restore` never worry about the stop/start dance: Servo probes status, stops an online server
first, runs your verb, and restores the prior state afterwards (it never starts a server someone
stopped on purpose).

Timeouts (Servo kills the whole process group on expiry): fast verbs (`describe`, `deps`,
`status`, `notify`, `players`, `metrics`, `version`s) get 30 seconds, `start`/`stop` get 10 minutes,
`install`/`update`/`backup`/`restore`/`uninstall` get 60 minutes.

## `start` must escape Servo's cgroup

Servo normally runs as a systemd user service, and systemd's default `KillMode=control-group`
kills **every process in the service's cgroup** when the service stops — including anything your
`start` verb spawned. Without countermeasures, a Servo stop, restart, or self-update takes the
game server down with it, ungracefully (no save-on-shutdown).

Convention: launch long-running processes in their own transient scope:

```sh
systemd-run --user --collect --scope -- podman start my-container
```

This needs the session dbus socket (`$XDG_RUNTIME_DIR/bus`), which exists under any normal
systemd user session. Verify on your host:

```sh
systemd-run --user --collect --scope -- true && echo ok
```

Only processes that must outlive the verb need this — short-lived work inside a verb (pulls,
tar, RCON calls) is fine as-is. See `fedora-palworld.sh`'s `start` verb for a real example with
a loud fallback when scope creation fails.

## Switching drivers & full reset

Servo runs one game server at a time. Activating a different driver is refused while the current
driver's server is online — stop it from the dashboard first. Switching never deletes anything:
the old driver's data/backups/etc stay where they are until you act.

For a full server reset (or before retiring a driver), use the dashboard's **Uninstall** button
(admin): it stops the server, runs the driver's `uninstall` verb (which should remove containers,
images, and anything else `install` created), then deletes the driver's data dir. Backup archives
are deliberately kept — restore-after-reinstall is the recovery path. If a driver doesn't
implement `uninstall`, the operation fails and cleanup is manual: read the driver to see what
`install` created, undo that, and remove `~/.servo/driver-data/<driver-name>/`.

## `describe` output

```
DRIVER_API=1
NAME=Palworld (Podman, Fedora)
GAME=palworld
CONTAINERIZED=true
TARGET_SERVER_VERSION=v0.6.4      # optional
TARGET_CONTAINER_VERSION=v1.2.3   # optional
```

- `DRIVER_API` (required): must be `1`. Servo refuses to activate drivers speaking a future API.
- `NAME` (required): human-readable, shown in the UI.
- `GAME`, `CONTAINERIZED`: informational.
- `TARGET_*_VERSION` (optional): the versions this driver was written/tested against. When they
  differ from the live `version`/`container-version` output, the dashboard shows a small "driver
  behind" badge. Purely informational, never a gate — plain string comparison, no semver.
- Unknown keys are ignored (forward compatibility).

## Practical advice

- Write for `sh`, not bash, unless you `deps`-check for bash.
- Make `start`/`stop` idempotent — Servo relies on it.
- Keep machine-readable data on stdout and diagnostics on stderr. Servo parses
  only stdout for probe verbs while retaining both streams in failure details.
- `backup` must print the archive path as its **last** stdout line; log lines before it are fine.
- Keep `describe`/`deps`/`status` fast and side-effect free; `status` is polled every few seconds
  while the dashboard is open.
- Test from the shell — that's the point of the design:

```sh
SERVO_DATA_DIR=/tmp SERVO_BACKUP_DIR=/tmp SERVO_VERSION=dev ./my-game.sh status; echo $?
```
