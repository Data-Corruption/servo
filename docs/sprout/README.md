# Sprout template docs (archived)

Servo is built on the [Sprout](https://github.com/Data-Corruption/sprout) template. These docs
came with the template and are written from Sprout's perspective ("use this template", placeholder
app names, mirror hosting, etc.). They're kept for reference — much of the plumbing they describe
(LMDB config, session auth, self-update, release pipeline) is exactly what runs inside Servo.

- [ARCHITECTURE.md](ARCHITECTURE.md) — the template's architecture: App container, LMDB, daemon
  lifecycle, self-update mechanism, transport & auth model.
- [DEVELOPMENT.md](DEVELOPMENT.md) — template setup: CI/CD, Cloudflare R2 release bucket,
  `build.sh` configuration.
- [MIRRORING.md](MIRRORING.md) — hosting byte-for-byte mirrors of release artifacts.

For Servo development, start at [CONTRIBUTING.md](../../CONTRIBUTING.md) and
[docs/DESIGN.md](../DESIGN.md) instead.
