# Changelog

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
