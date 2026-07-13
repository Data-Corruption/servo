# Installing Servo

Servo runs on Linux (`amd64`/`arm64`) with `systemd --user`. Install it on the host that will run
the game server.

## Install

```sh
curl -fsSL https://cd.servo.regfile.net/install.sh | sh
```

The script verifies every artifact it downloads: it fetches `checksums.txt` plus its cosign
bundle, verifies the signature against the release CI identity, then sha256-matches the binary.
Requires `cosign` ([install instructions](https://docs.sigstore.dev/cosign/system_config/installation/)).

For higher assurance, verify the install script itself before running it:

```sh
curl -fsSL https://cd.servo.regfile.net/install.sh -o install.sh
curl -fsSL https://cd.servo.regfile.net/install.sh.cosign.bundle -o install.sh.cosign.bundle
cosign verify-blob \
  --certificate-identity "https://github.com/Data-Corruption/servo/.github/workflows/release.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle install.sh.cosign.bundle install.sh
sh install.sh
```

Self-updates use the same verification: before applying an update, Servo re-downloads the install
script and cosign-verifies it against the identity baked in at build time.

## First login

The dashboard requires a password. After installing, create a credential:

```sh
servo password add --label admin
```

You'll be prompted for the password without echo. Then open `https://localhost:8829` (or your
configured port).

> [!NOTE]
> The dashboard always serves HTTPS with a self-signed certificate, so your browser will show a
> warning on first visit — expected for a local service; proceed/accept to continue. The
> certificate is generated once and reused, so the warning is a one-time event per browser.

### Behind a reverse proxy

If you want a real certificate and a proper domain, run a TLS-terminating reverse proxy on the
same host. Servo runs a plain-HTTP proxy listener on `127.0.0.1:8830` by default — loopback only,
non-loopback binds are rejected, and it can be re-bound or disabled (empty bind) from the
dashboard settings. Example Caddyfile:

```
servo.example.com {
    reverse_proxy 127.0.0.1:8830
}
```

## Uninstall

```sh
servo uninstall
```
