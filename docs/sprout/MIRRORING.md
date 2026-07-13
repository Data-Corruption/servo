# Mirroring Guide

Organizations that require controlled distribution — compliance review, vulnerability scanning, internal approval gates, or air-gapped-ish networks — can host their own mirror of the release artifacts. The release layout is intentionally flat (no nested paths, no API calls), so a mirror is just a static file host.

The key property that makes this work: **all cosign signatures are URL-independent**. Verification is pure content + signer identity; nothing about the download location is signed. A byte-for-byte copy of the official artifacts verifies identically from any host.

## What a release artifact set is

```
install.sh                    # installer, cosign-signed
install.sh.cosign.bundle
install.ps1                   # Windows/WSL installer, cosign-signed
install.ps1.cosign.bundle
linux-amd64.gz                # gzipped static binaries
linux-arm64.gz
checksums.txt                 # sha256 of binaries + version, cosign-signed
checksums.txt.cosign.bundle
version                       # latest version marker (also listed in checksums.txt)
```

## Operating a mirror

Copy the official artifact set, unmodified, to your host:

```sh
BASE=https://cd.example.com/   # official release URL
for f in install.sh install.sh.cosign.bundle install.ps1 install.ps1.cosign.bundle \
         linux-amd64.gz linux-arm64.gz checksums.txt checksums.txt.cosign.bundle version; do
  curl -fsSLO "$BASE$f"
done
# upload the files to your static host, byte-for-byte
```

Do not edit anything. Editing any file breaks its signature **by design** — the artifacts chain to the official CI identity, and that chain is the whole point. Validate however your organization requires (scanning, testing) before publishing, and keep archived artifact sets per your audit retention policy.

Updating the mirror = copying the new release set the same way. Your mirror is the record of which version your users are approved to run.

## Installing from a mirror

Users point the official installer at the mirror with an environment variable:

```sh
curl -fsSL https://mirror.example.com/install.sh -o install.sh
# optional but recommended: verify the script against the OFFICIAL identity first
cosign verify-blob \
  --certificate-identity "https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  --bundle install.sh.cosign.bundle install.sh
APP_RELEASE_URL=https://mirror.example.com/ sh install.sh
```

The script's internal artifact verification works unchanged — it downloads `checksums.txt` + bundle from the mirror, cosign-verifies it against the official identity, and sha256-matches the binary.

## Mirror installs do not auto-update (by design)

`install.sh` only writes the `release-url` file — the thing update checking and remote self-update read — when the effective URL equals the official baked `RELEASE_URL`. An `APP_RELEASE_URL` override means no `release-url` file, and the app treats that as "updates disabled" (a silent no-op, not an error).

This is deliberate, for two concrete reasons:

- Remote self-update executes the release host's `install.sh`. Pointing that at a third-party host would hand the mirror operator code execution on every update.
- Mirrors exist to pin approved versions; auto-update would defeat that.

Updating a mirror install: the operator copies the new release set to the mirror, users re-run the install command above. As a backstop, even a forced-on update from a mirror would fail safe: the app cosign-verifies the downloaded `install.sh` against the official identity before executing it, so a modified script never runs.

## Fallback: modified installer scripts

If you genuinely must customize `install.sh` (discouraged), the signature model degrades gracefully rather than silently:

- Your modified script no longer verifies against the official identity. Re-sign it with your own cosign identity and tell your users to verify against *you* instead.
- The artifact verification inside the script still chains to the official identity — the binaries are still ours, and your users now trust two identities: yours for the script, ours for the artifacts.
- Do not force-write the `release-url` file from a modified script. Remote updates from your host will fail cosign verification at update time anyway.
