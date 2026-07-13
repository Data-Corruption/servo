# Development Guide

## Prerequisites

- **Go**: Version 1.25 or higher
- **Zig**: Used as the C cross-compiler (`zig cc`) for fully static musl release binaries (amd64 + arm64). Release binaries run on any distro, including NixOS.
- **gcc**: Only needed for non-fast local builds (`go test -race`).
- **Environment**: Linux or WSL (Windows Subsystem for Linux). On NixOS, use the repo flake: `nix develop`.
- **Architecture**: `amd64` / `x86_64` runner for local verification. The build script also produces `linux-arm64` release artifacts.

The build script downloads its remaining tools (tailwindcss standalone, daisyui bundles, esbuild) into the gitignored `tools/` directory automatically.

## Architecture

Before diving into the code, check out [ARCHITECTURE.md](ARCHITECTURE.md) to understand the high-level design, core components, and data flow.

This CI/CD pipeline is built on GitHub Actions and Cloudflare R2. The Cloudflare R2 bucket stores release artifacts. If you want the old self-hosted Forgejo runner setup with the aggressive cached checkout flow, see the Codeberg version of this repo: `REPLACE_WITH_CODEBERG_URL`

## Steps

### 1. Use this Template
Click the "Use this template" button on GitHub to create a new repository based on Sprout.

### 2. Enable GitHub Actions

**Repo Settings → Actions → General → Workflow permissions:** Read and write permissions

**Repo Settings → Secrets and variables → Actions:** Create the variable `CI_ENABLED` = `true`

The workflow itself (`.github/workflows/release.yml`) declares least-privilege permissions: `permissions: {}` at the workflow level, with `contents: write` (tag push) and `id-token: write` (cosign keyless signing) on the release job only. All actions are SHA-pinned. Optional repository variables: `VERBOSE`, `TAILWIND_VERSION`, `DAISYUI_VERSION`, and `REFETCH_TOOLS` (set to force re-downloading the cached frontend tools).

### 3. Setup Cloudflare R2

This assumes you have a domain and Cloudflare account. If you don't, get one. 

This project is setup so you can swap this part out if you want. This is all handled in `scripts/build.sh` using runner secrets for upload auth. The release host serves a simple flat directory. On official installs, `install.sh` persists the release URL into `~/.APP_NAME/release-url` so later update checks keep using the approved source (mirror installs skip this — see [MIRRORING.md](MIRRORING.md)):
```
release/
  install.sh
  install.sh.cosign.bundle
  install.ps1
  install.ps1.cosign.bundle
  linux-amd64.gz
  linux-arm64.gz
  checksums.txt
  checksums.txt.cosign.bundle
  version
```

All artifacts are signed with **cosign keyless signing**: the release workflow's OIDC identity (`https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main`) is exchanged for a short-lived certificate, and each `.cosign.bundle` carries the signature, certificate, and Rekor transparency-log proof. `install.sh` verifies `checksums.txt` against this identity, then sha256-matches the binary it downloads. No key management needed — but never rename or move `.github/workflows/release.yml`, its path *is* the trust anchor.

Go to Cloudflare dashboard, create an account if you don't have one. Get a domain if you don't have one.

In the main dashboard, select **Storage & databases → R2 object storage** (sign up for free tier, will be fine for small / medium projects. You can switch to self host later easily) → **Overview → Create bucket**.
- Name: `YOUR-APP-cd`
- Region: `Auto`
- Default Storage Class: `Standard`

After creation, **Bucket Settings → Custom Domains → Add**:  
`cd.yourdomain.com`

In the dashboard, select **Account home**, then the domain you want to use. Now select **Rules → Overview → Create rule → Cache Rule**.
- Name: `Bypass cache for YOUR-APP CD`
- Custom filter expression - When incoming requests match...
  - Field: `Hostname`
  - Operator: `equals`
  - Value: `YOUR-APP-cd.yourdomain.com`
- Then
  - Action: `Bypass cache`

In **R2 object storage → Overview** on the right under Account Details, click **{}Manage** API Tokens. Kinda easy to miss. **Create User API Token**:
- Token Name: `YOUR-APP CD`
- Permissions: `Object Read & Write`

After creation, copy the:
- Access Key ID
- Secret Access Key

Back in the **R2 object storage → Overview** Account Details
- Copy the Account ID
- Copy the Bucket Name e.g. `YOUR-APP-cd`

Open your repository, **Settings → Actions → Secrets** Add the following secrets:
- `R2_ACCESS_KEY_ID` = paste Access Key ID
- `R2_SECRET_ACCESS_KEY` = paste Secret Access Key
- `R2_ACCOUNT_ID` = paste Account ID
- `R2_BUCKET` = paste Bucket Name

### 4. Clone your new repository
```sh
  git clone https://github.com/YOUR_USERNAME/YOUR_REPO.git
  cd YOUR_REPO
```

### 5. Configure the Template
All configuration is done at the top of `scripts/build.sh`:
- `APP_NAME`: Your application name (binary name).
- `RELEASE_URL`: URL baked into the generated install scripts, e.g. `https://cd.yourdomain.com/release/`. On official installs the installer writes this into the app's `release-url` file, which is then used for update checks and self-updates.
- `CONTACT_URL`: This is used in the User-Agent. It's currently unused, but if you start making requests to other services it's a good idea to add it to the request headers. Your apps landing page or repo URL is fine.
- `OIDC_ISSUER`: Cosign OIDC issuer. Leave as the GitHub Actions default unless you move CI elsewhere. `CERT_IDENTITY` is derived automatically in CI from the repository and workflow path — you don't set it by hand.
- `DEFAULT_LOG_LEVEL`: The default log level (e.g. `debug`, `info`, `warn`, `error`).
- `SERVICE`: Set to "true" or "false" to enable/disable the daemon.
- `SERVICE_DESC`: Description for the systemd service.
- `SERVICE_ARGS`: Arguments to pass to the binary when running as a daemon. Unless you have a specific reason, leave this as `service run`.
- `SERVICE_DEFAULT_PORT`: The default port the HTTPS dashboard listens on (e.g. `8484`, becomes the default `UIBind` of `:8484`).

### 6. **Build the project**:
   ```sh
   ./scripts/build.sh
   ```

   For a quicker local smoke build on the current machine, you can skip tests and build only the host architecture:
   ```sh
   ./scripts/build.sh --fast
   ```

   For local development with zero auth friction, use a test build:
   ```sh
   ./scripts/build.sh --test --fast
   ```
   Test builds bake `TestMode` into the binary: HTTP auth is bypassed (every request gets admin), storage/runtime dirs get a `-test` suffix (`~/.APP_NAME-test`) so a dev build never touches a real install's data, and logging is forced to debug. The `--test` flag is local-only; CI never sets it.

### 7. **Test it**:
   ```sh
   ./out/linux-amd64 service run
   ```

   Then open `https://localhost:8484`. The dashboard is always HTTPS with a self-signed cert, so the browser will warn on first visit — accept it for local dev. Non-test builds require a login credential first:
   ```sh
   ./out/linux-amd64 password add --label admin
   ```
   You'll be prompted for the password without echo. Scope credentials with `--perms`, e.g. `--perms "admin !game.restore"` (all permissions except restoring backups) or `--perms "game.control game.backup"`. `password list` and `password remove --label X` round out the command. Permissions are a bitmask defined in `internal/types/perms.go`.

Dev (non CI) builds set the app version to `v.X.X.X` which disables update related features. This is useful for testing / conditionally enabling things you don't want in dev.

## Release Workflow

This project uses a changelog-driven release process:

1. Insert an entry to `CHANGELOG.md` under # Changelog, describing your changes. See [CHANGELOG.md](../../CHANGELOG.md) for example.
2. Push your changes to the `main` branch.
3. GitHub Actions will automatically build the project and upload it to the release bucket. Users should see the update within a day or so.

To see how the update process works, see the [settings page](../../internal/platform/http/router/settings/settings.go).  
To test it:
- publish a new release
- run `YOUR_APP update --check` to force a check, otherwise it will wait and only check ~once a day.
- visit/refresh `https://localhost:8484` in your browser.
- you should see a notification about an update. Click **restart → enable update → confirm** and the app will update, just like magic ✨

Before executing a remote update, the app re-downloads `install.sh` and cosign-verifies it against the identity baked at build time — a tampered or mirror-modified script fails verification and the update aborts. The update code is also structured as clearly fenced, delete-to-disable blocks; see the "Self-Update Mechanism" section of [ARCHITECTURE.md](ARCHITECTURE.md) for the exact removal recipes.

## Mirrors

Third parties can host byte-for-byte mirrors of the release artifacts on any static file host — all cosign signatures remain valid because they are URL-independent. Mirror installs automatically get no update checking or remote updates (the installer only writes `release-url` for official-URL installs). See [MIRRORING.md](MIRRORING.md) for the full operator and user guide.
