#!/bin/sh
# Servo driver: Palworld via podman on Fedora (Driver API v1)
#
# Drives https://github.com/thijsvanloef/palworld-server-docker rootless.
# Install: scp fedora-palworld.sh host:~/.servo/drivers/ && chmod +x it,
# then activate in the Servo settings page and press Install once.
#
# Edit the config section below (passwords!) before installing.
#
# Rootless podman notes:
#   - The container does NOT auto-start on host reboot. Start it from the
#     Servo dashboard, or wire up Quadlet yourself if you care.
#   - Ports >1024 only (8211/27015 are fine).
#   - -v ...:Z relabels the data dir for SELinux (Fedora default: enforcing).

set -u

# --- config (edit me) ----------------------------------------------------

IMAGE="docker.io/thijsvanloef/palworld-server-docker:latest"
CONTAINER="palworld-server"
DATA="$SERVO_DATA_DIR/palworld"

GAME_PORT=8211
QUERY_PORT=27015
PLAYERS=16
SERVER_NAME="Servo Palworld"
SERVER_PASSWORD="changeme"       # empty = no password
ADMIN_PASSWORD="changeme-admin"  # also the RCON password
STOP_TIMEOUT=90                  # seconds; Palworld saves on shutdown

# When you verify this driver against a specific image release, pin it here
# to power Servo's (soft) staleness badge. Empty = badge disabled.
TARGET_CONTAINER_VERSION=""

# --- helpers ------------------------------------------------------------

need() {
  _missing=0
  for _tool in "$@"; do
    if ! command -v "$_tool" >/dev/null 2>&1; then
      echo "$_tool"
      _missing=1
    fi
  done
  return $_missing
}

container_exists() { podman container exists "$CONTAINER"; }

container_running() {
  [ "$(podman inspect --format '{{.State.Running}}' "$CONTAINER" 2>/dev/null)" = "true" ]
}

# rcon-cli is bundled inside the image; only works while running
rcon() { podman exec "$CONTAINER" rcon-cli "$@"; }

create_container() {
  mkdir -p "$DATA"
  podman create --name "$CONTAINER" \
    -p "$GAME_PORT:8211/udp" \
    -p "$QUERY_PORT:27015/udp" \
    -v "$DATA:/palworld:Z" \
    -e PUID=1000 -e PGID=1000 \
    -e PORT=8211 \
    -e PLAYERS="$PLAYERS" \
    -e SERVER_NAME="$SERVER_NAME" \
    -e SERVER_PASSWORD="$SERVER_PASSWORD" \
    -e ADMIN_PASSWORD="$ADMIN_PASSWORD" \
    -e RCON_ENABLED=true \
    -e RCON_PORT=25575 \
    -e COMMUNITY=false \
    "$IMAGE"
}

# --- verbs ----------------------------------------------------------------

case "${1:-}" in

describe)
  echo "DRIVER_API=1"
  echo "NAME=Palworld (Podman, Fedora)"
  echo "GAME=palworld"
  echo "CONTAINERIZED=true"
  [ -n "$TARGET_CONTAINER_VERSION" ] && echo "TARGET_CONTAINER_VERSION=$TARGET_CONTAINER_VERSION"
  exit 0
  ;;

deps)
  need podman tar gzip
  ;;

status)
  container_running && exit 0 || exit 3
  ;;

start)
  if container_running; then
    echo "already running"
    exit 0
  fi
  if ! container_exists; then
    echo "container not found — run install first" >&2
    exit 1
  fi
  podman start "$CONTAINER"
  ;;

stop)
  if ! container_running; then
    echo "already stopped"
    exit 0
  fi
  podman stop -t "$STOP_TIMEOUT" "$CONTAINER"
  ;;

install)
  echo "pulling $IMAGE ..."
  podman pull -q "$IMAGE" || exit 1
  if container_exists; then
    echo "container already exists, recreating"
    podman rm -f "$CONTAINER" || exit 1
  fi
  create_container || exit 1
  echo "installed. data dir: $DATA"
  ;;

update)
  # convention: check first, no-op success if already current
  echo "pulling $IMAGE ..."
  podman pull -q "$IMAGE" || exit 1
  new_id=$(podman image inspect --format '{{.Id}}' "$IMAGE") || exit 1
  cur_id=$(podman inspect --format '{{.Image}}' "$CONTAINER" 2>/dev/null || echo none)
  if [ "$new_id" = "$cur_id" ]; then
    echo "already up to date"
    exit 0
  fi
  echo "new image found, recreating container (data dir is preserved)"
  container_exists && { podman rm -f "$CONTAINER" || exit 1; }
  create_container || exit 1
  echo "updated"
  ;;

backup)
  [ -d "$DATA" ] || { echo "no data dir to back up" >&2; exit 1; }
  f="$SERVO_BACKUP_DIR/palworld-$(date +%Y%m%d-%H%M%S).tar.gz"
  echo "archiving $DATA ..."
  tar -czf "$f" -C "$SERVO_DATA_DIR" palworld || { rm -f "$f"; exit 1; }
  echo "$f"
  ;;

restore)
  archive="${2:?archive path required}"
  [ -f "$archive" ] || { echo "archive not found: $archive" >&2; exit 1; }
  echo "wiping $DATA and restoring from $archive ..."
  rm -rf "$DATA"
  tar -xzf "$archive" -C "$SERVO_DATA_DIR" || exit 1
  echo "restored"
  ;;

notify)
  msg="${2:?message required}"
  container_running || { echo "server offline, skipping notify" >&2; exit 1; }
  # Palworld's Broadcast mangles spaces; underscores are the accepted hack
  rcon "Broadcast $(echo "$msg" | tr ' ' '_')"
  ;;

players)
  container_running || exit 1
  # ShowPlayers prints "name,playeruid,steamid" with a header line.
  # NOTE: names containing commas will be truncated — cosmetic only.
  rcon ShowPlayers | tail -n +2 | cut -d',' -f1 | sed '/^$/d'
  ;;

container-version)
  ver=$(podman image inspect --format '{{index .Config.Labels "org.opencontainers.image.version"}}' "$IMAGE" 2>/dev/null)
  [ -n "$ver" ] || exit 1
  echo "$ver"
  ;;

version)
  # Palworld doesn't expose its server version cleanly; decline.
  exit 4
  ;;

*)
  echo "unknown verb: ${1:-<none>}" >&2
  exit 4
  ;;
esac
