# Changelog

## [v0.2.2] - 2026-07-14

Added

- Driver convention (docs + template): `start` must launch long-running processes outside Servo's service cgroup (`systemd-run --user --collect --scope -- ...`). systemd's default `KillMode=control-group` otherwise kills the game server — ungracefully — on any Servo stop/restart/self-update. `fedora-palworld.sh` now follows the convention (falls back to an in-cgroup start with a loud warning if scope creation fails) and deps-checks `systemd-run`.
- Warnings on Servo stop/restart in the settings UI and on the `update` CLI command: a game server started inside Servo's service dies with it if the driver doesn't detach.

## [v0.2.1] - 2026-07-14

Added
- Confirmation dialogs for Install and Update on the dashboard (Stop and Uninstall already confirmed). Install warns that a re-run may force-stop a running server; Update notes the graceful stop / recreate / restart-if-was-online behavior.
- `fedora-palworld.sh`: configurable `COMMUNITY` flag. When false (default), the query port is not published on the host; when true, it is published for the in-game community browser. Toggling or changing container env settings still requires pressing Install to recreate the container (data dir preserved).

## [v0.2.0] - 2026-07-14

Added
- Optional `uninstall` driver verb (Driver API v1): full server teardown from the dashboard (admin). Servo stops the server if needed, the driver removes everything it created outside its data dir (containers, images, ...), then Servo deletes the driver's data dir. Backup archives are kept — restore-after-reinstall is the recovery path. Drivers that don't implement the verb decline with exit 4 and the operation fails with a clear message.
- `uninstall` implemented in `fedora-palworld.sh` (removes the container and image) and stubbed in `driver.template.sh`.

Changed
- `SERVO_DATA_DIR` and `SERVO_BACKUP_DIR` are now exclusive per-driver subdirectories (`driver-data/<driver>/`, `backups/<driver>/`), named after the driver file and created by Servo. Drivers no longer need to carve out their own subdir — use the dirs directly. Backup listing, download, restore, and retention pruning are all scoped to the active driver, so switching games can no longer prune or restore another game's archives. Renaming a driver file orphans its dirs. No migration: existing flat-layout data/backups are not picked up.
- Driver activation is guarded: refused while an operation is running or while the current driver's server is online. Stop the server before switching; a broken driver that fails its status probe never blocks the switch.

## [v0.1.0] - 2026-07-13

Initial release: a dashboard for managing a dedicated game server, sized for small friend groups.

Added
- Driver contract (Driver API v1): all game-specific logic lives in a single external executable implementing a small verb set (`describe`, `deps`, `status`, `start`, `stop`, `install`, `update`, `backup`, plus optional `restore`, `notify`, `players`, `version`, `container-version`). Drivers are installed over SSH into `~/.servo/drivers/` and activated (with `describe`/`deps` validation) from the dashboard, admin only.
- Reference driver `fedora-palworld.sh` (Palworld via rootless podman on Fedora) and `driver.template.sh` for authoring new drivers.
- Web dashboard: server status and player roster polling, start/stop/restart/update controls, backups list with download and one-click restore, join-info copy buttons, and an activity panel showing the current operation (admins see live driver output).
- Single-flight operation runner: one driver operation at a time, async with a persisted last-result and an in-memory log ring buffer.
- Daily restart window: optional in-game player warning via `notify` (configurable lead time), optional pre-restart backup, and never starts a server someone stopped on purpose.
- Backups: driver-produced single compressed archives, retention-pruned, downloadable and restorable from the dashboard.
- Permission bitmask split into `game.*` (control, backup, restore) and `servo.*` (settings, control) namespaces plus `admin`; credentials created via `servo password add --perms`.
- Appearance settings: custom login/dashboard background images (admin upload), blur, content alignment, and a global theme override.
- Inherited from the Sprout template: always-TLS dashboard with optional loopback reverse-proxy listener (default `127.0.0.1:8830`), session auth with Argon2id-hashed credentials, LMDB-backed config shared by CLI and daemon, systemd user service, and cosign-verified installs and self-updates.
