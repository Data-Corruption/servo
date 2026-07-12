# Installation Guide Template

*This file is a template. You should update it to provide instructions for **your** users once you have configured the project.*

Replace `<YOUR_RELEASE_URL>` with the same URL you set in the build script.  
Replace `<YOUR_APP_NAME>` with the name of your app.  
Replace `<OWNER>/<REPO>` with your GitHub repository.

> [!NOTE]
> On official installs, the installer persists its source URL into `~/.<YOUR_APP_NAME>/release-url`, and future update checks/self-updates use that file. Installs from a mirror (`APP_RELEASE_URL=... sh install.sh`) skip this and get no automatic updates — see [MIRRORING.md](MIRRORING.md).

---

## Installation

To install the latest version, run the following install command:

**Linux**
```sh
curl -fsSL <YOUR_RELEASE_URL>install.sh | sh
```

The script verifies every artifact it downloads: it fetches `checksums.txt` plus its cosign bundle, verifies the signature against the release CI identity, then sha256-matches the binary. Requires `cosign` ([install instructions](https://docs.sigstore.dev/cosign/system_config/installation/)).

For higher assurance, verify the install script itself before running it:

```sh
curl -fsSL <YOUR_RELEASE_URL>install.sh -o install.sh
curl -fsSL <YOUR_RELEASE_URL>install.sh.cosign.bundle -o install.sh.cosign.bundle
cosign verify-blob \
  --certificate-identity "https://github.com/<OWNER>/<REPO>/.github/workflows/release.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle install.sh.cosign.bundle install.sh
sh install.sh
```

**Windows**
```powershell
Set-ExecutionPolicy Bypass -Scope Process -Force; iex "& { $(irm <YOUR_RELEASE_URL>install.ps1) }"
```

> [!IMPORTANT]
> **Windows/WSL Support is Experimental.** 
> The Windows installer uses WSL to run the application. While functional, it may be finicky on some systems. If you run into issues, try running `wsl --update` and then re-run the installer.
>
> The Windows path is also lower-assurance: `install.ps1` pipes the Linux install script into WSL without cosign-verifying it first (the artifact verification inside WSL still runs). If that matters for your threat model, install inside WSL directly using the verified Linux instructions above.

---

## First Login

The web dashboard requires a password. After installing, create a credential:

```sh
<YOUR_APP_NAME> password add --label admin
```

You'll be prompted for the password without echo. Then open `https://localhost:8484` (or your configured port).

> [!NOTE]
> The dashboard always serves HTTPS with a self-signed certificate, so your browser will show a warning on first visit — this is expected for a local service; proceed/accept to continue. The certificate is generated once and reused, so the warning is a one-time event per browser.

### Behind a reverse proxy

If you want a real certificate and a proper domain, run a TLS-terminating reverse proxy on the same host and enable the plain-HTTP proxy listener in the dashboard settings (e.g. `127.0.0.1:8485` — loopback only, non-loopback binds are rejected). Example Caddyfile:

```
app.example.com {
    reverse_proxy 127.0.0.1:8485
}
```

---

## Uninstall

To uninstall the app, simply run:

```sh
<YOUR_APP_NAME> uninstall
```
