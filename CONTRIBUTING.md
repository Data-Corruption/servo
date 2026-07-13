# Contributing to Servo

Quickstart for hacking on Servo itself. If you just want to run a game server, see
[docs/INSTALLATION.md](docs/INSTALLATION.md); if you want to support a new game, you probably
don't need to touch Servo at all — write a driver instead ([docs/DRIVERS.md](docs/DRIVERS.md)).

## Prerequisites

- **Go** 1.25+
- **Zig** — C cross-compiler (`zig cc`) for the fully static musl release binaries (amd64 + arm64)
- **gcc** — only for non-fast builds (`go test -race`)
- Linux or WSL. On NixOS, use the repo flake: `nix develop`

The build script downloads its remaining tools (tailwindcss standalone, daisyui, esbuild) into the
gitignored `tools/` directory automatically.

## Dev loop

```sh
./scripts/build.sh --test --fast   # skip tests, host arch only, TestMode baked in
./out/linux-amd64 service run      # run the daemon in the foreground
```

Then open `https://localhost:8829` (self-signed cert — accept the browser warning). Test builds
bypass auth entirely (every request gets admin) and isolate storage in `~/.servo-test`, so a dev
build never touches a real install's data. Logging is forced to debug.

Other build modes:

- `./scripts/build.sh` — full build: frontend assets, `go test -race ./...`, both arches.
- `./scripts/build.sh --fast` — skip tests, host arch only, but real auth (create a login with
  `./out/linux-amd64 password add --label admin`).

Dev builds set the version to `vX.X.X`, which disables update-related features.

To exercise a driver without the UI, just run it by hand — that's the point of the contract:

```sh
./drivers/fedora-palworld.sh status; echo $?
```

## Where things live

Read [docs/DESIGN.md](docs/DESIGN.md) first — it covers the driver contract, job model,
scheduler, and security model, and explains most of the "why".

```
cmd/main.go                      # entry point: creates App, registers commands
internal/app/                    # App struct (DI container), lifecycle, self-update
│   └── commands/                # CLI subcommands (service, password, update, uninstall)
internal/driver/                 # driver invocation: verbs, exit codes, describe parsing
internal/ops/                    # operation runner (single-flight), status poller,
│                                #   restart/backup scheduler, backup retention
internal/platform/database/      # LMDB wrapper, config accessors, migrations
internal/platform/http/          # server (two listeners), session auth middleware, routers
│   └── router/                  #   dashboard, api, settings, login
internal/types/                  # Configuration struct, permission bitmask (perms.go)
internal/ui/                     # embedded templates + assets (Tailwind/DaisyUI, esbuild)
pkg/                             # reusable bits: crypto (Argon2id), migrator, sdnotify
drivers/                         # driver.template.sh + reference fedora-palworld.sh
scripts/build.sh                 # app name, ports, release URL, signing config
```

Common recipes:

- **New CLI command**: add a file in `internal/app/commands/` using the self-registering
  `register(func(a *app.App) *cli.Command {...})` pattern — no manual list editing.
- **New HTTP route**: create a package under `internal/platform/http/router/`, define
  `Register(a *app.App, r chi.Router)`, mount it in `router.go`. Routes sit behind session auth
  automatically (only `/login` and `/assets/` are exempt); gate state-changing handlers with
  `middleware.RequirePerm(r, types.PermX)`.
- **New config field**: add it to `internal/types/types.go` (+ `DefaultConfig`), and add a
  migration in `internal/platform/database/migration.go` if existing installs need it populated.
- **Frontend**: JS modules live in `internal/ui/assets/js/src/` (imported from `main.js`,
  bundled via esbuild); styles come from Tailwind + DaisyUI via `assets/css/input.css`. Assets
  are hashed and embedded at build time for cache busting.

> [!WARNING]
> Some HTML formatters mangle Go template syntax (inserting spaces inside `{{ }}`), so this repo
> disables format-on-save for HTML files in `.vscode/settings.json`.

## Releasing

Changelog-driven: add an entry to [CHANGELOG.md](CHANGELOG.md), push to `main`, and GitHub
Actions builds, signs (cosign keyless), and uploads the release. Installs pick up the update
within a day, or immediately via `servo update --check`.

The release pipeline, R2 bucket setup, and update internals are inherited from the Sprout
template — see [docs/sprout/](docs/sprout/) if you need to dig into that plumbing.
