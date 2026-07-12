# Changelog

<!--
Replace with your changelog entries. You can start at any version that isn't already tagged.
Remove the leading "//" from the example entries. It's to keep the CI from thinking they're real.

// ## [v1.1.0] - 2026-07-11

Security & infrastructure overhaul (backported from a production app built on Sprout).

Added
- Cosign keyless signing for all release artifacts; install.sh verifies checksums.txt signature then sha256-matches binaries. Remote self-update cosign-verifies the install script before executing it.
- Session auth for the dashboard: login page, Argon2id-hashed credentials via `password add/list/remove` CLI, permission bitmask, in-memory sliding sessions, same-origin CSRF guard, rate limiting.
- Always-TLS two-listener server: HTTPS dashboard with auto-generated self-signed cert (UIBind), optional loopback-only plain HTTP listener for reverse proxies (ProxyBind). Replaces Port/Host/ProxyPort config.
- Test mode (`build.sh --test`): auth bypass, isolated "-test" storage/runtime dirs, forced debug logging. Local builds only.
- Byte-for-byte mirror support via APP_RELEASE_URL with URL-independent signatures; mirror installs get updates disabled by design (docs/MIRRORING.md).
- Fully static musl release binaries via zig cc (run on any distro incl. NixOS); nix flake for dev shells.

Changed
- Update code restructured into fenced, delete-to-disable blocks (UPDATE CHECK / REMOTE UPDATE / UPDATE SHARED) with documented removal recipes.
- CI: build.yml replaced by release.yml — SHA-pinned actions, least-privilege permissions, concurrency guard. The workflow path is the cosign trust anchor; never rename it.
- Release layout: per-file .sha256 files replaced by a single signed checksums.txt; .cosign.bundle files added.
- Frontend: unified requestJSON API helper, event-listener wiring (no window.* globals or inline onclick), shared page shell partials, un-busted asset route aliases for CSS url() refs.

// ## [v1.0.1] - 2025-12-06

Example update.

// ## [v1.0.0] - 2025-11-24

Minimal starter for Go CLI apps with an optional webserver daemon, changelog‑driven Github Actions CI/CD, and self‑updating installs.

Added
- CLI scaffold using urfave/cli v3 with common flags and subcommands.
- Service subcommand running an HTTP server (default :8383); installer provisions a systemd service.
- Shared atomic data/config directory via LMDB, safely used by both CLI and service.
- Intuitive migration system for data/config.
- Structured, rotatable logging via stdx/xlog under the per-user data path.
- Changelog-driven release automation; daily lightweight version checks and an update command with opt-out notifications.
- Cross-platform installers:
  - Linux installer with optional version pinning.
  - Windows PowerShell (WSL) installer.
- Build scripts for reproducible, versioned artifacts.
- Apache-2.0 license and template documentation.
- Build time variable injection via LDFLAGS with verification tests.
- Tests for most of the important parts of the codebase (updating, migrations, etc).

Notes
- Local builds use a placeholder version (vX.X.X) and skips update logic.
- Project structure using standard Go layout (`cmd`, `internal`, `pkg`).

-->