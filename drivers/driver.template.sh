#!/bin/sh
# Servo driver template (Driver API v1)
#
# A driver is a single executable that Servo invokes as:
#
#   <driver> <verb> [args...]
#
# To write one: copy this file, fill in the verbs, then install it on the
# host over SSH:
#
#   scp my-game.sh host:~/.servo/drivers/ && ssh host chmod +x ~/.servo/drivers/my-game.sh
#
# Environment provided by Servo on every invocation:
#   SERVO_BACKUP_DIR   where `backup` must write archives; exclusive to this
#                      driver (a subdir named after the driver file)
#   SERVO_DATA_DIR     scratch/persistent dir exclusive to this driver (a
#                      subdir named after the driver file); created by Servo,
#                      deleted by Servo after a successful `uninstall`
#   SERVO_VERSION      Servo's own version string
#
# Exit codes:
#   0   success (for `status`: server is online; for `deps`: all present)
#   3   `status` only: server is stopped. Not an error.
#   4   verb not supported (how optional verbs decline)
#   *   anything else is a failure; stdout/stderr is shown in the UI
#
# Orchestration guarantees (Servo's side of the contract):
#   - Only one verb runs at a time.
#   - `update`, `backup`, and `restore` are always called with the server
#     stopped; Servo handles the stop/start dance around them.
#   - `update` convention: check the current version first and succeed as a
#     no-op if already up to date.
#
# Full authoring guide: docs/DRIVERS.md in the Servo repo.

set -u

# --- helpers -----------------------------------------------------------------

# need <tool>... : print missing tools (one per line), used by `deps`
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

# --- verbs ---------------------------------------------------------------

case "${1:-}" in

describe)
  # Metadata. Must be fast and side-effect free. TARGET_* keys are optional;
  # when present they power the (soft) staleness badge in the UI.
  cat <<EOF
DRIVER_API=1
NAME=My Game (fill me in)
GAME=mygame
CONTAINERIZED=false
EOF
  # TARGET_SERVER_VERSION=v1.2.3
  # TARGET_CONTAINER_VERSION=v4.5.6
  ;;

deps)
  # List every external tool the other verbs use.
  need tar gzip
  ;;

status)
  # exit 0 = online, exit 3 = stopped, anything else = error
  echo "TODO: implement status" >&2
  exit 1
  ;;

start)
  # Idempotent: already running = success.
  echo "TODO: implement start" >&2
  exit 1
  ;;

stop)
  # Graceful, idempotent: already stopped = success.
  echo "TODO: implement stop" >&2
  exit 1
  ;;

install)
  # First-time setup: pull image, create dirs, etc.
  echo "TODO: implement install" >&2
  exit 1
  ;;

update)
  # Server is stopped. Check current version first; no-op success if current.
  echo "TODO: implement update" >&2
  exit 1
  ;;

backup)
  # Server is stopped. Write ONE compressed archive into $SERVO_BACKUP_DIR
  # and print its absolute path as the LAST line of stdout.
  echo "TODO: implement backup" >&2
  exit 1
  ;;

restore)
  # Optional. Server is stopped. $2 is an archive previously produced by
  # this driver's `backup`. Delete this case to decline (or keep exit 4).
  exit 4
  ;;

uninstall)
  # Optional. Server is stopped. Remove everything this driver created
  # OUTSIDE $SERVO_DATA_DIR (containers, images, units...). Servo deletes
  # $SERVO_DATA_DIR itself afterwards; backups are kept.
  exit 4
  ;;

notify)
  # Optional. Deliver "$2" to in-game players (RCON etc.).
  exit 4
  ;;

players)
  # Optional. Print one connected player name per line (count = line count).
  exit 4
  ;;

version)
  # Optional. Print the live game server version.
  exit 4
  ;;

container-version)
  # Optional. Print the live container image version/tag.
  exit 4
  ;;

*)
  echo "unknown verb: ${1:-<none>}" >&2
  exit 4
  ;;
esac
