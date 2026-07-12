#!/usr/bin/env bash

# Build script for local development and CI releases.
#
# Local:
#   ./scripts/build.sh
#     Build frontend assets, run tests, and build linux-amd64 + linux-arm64.
#
# Fast local:
#   ./scripts/build.sh --fast
#     Skip tests and build only the current host architecture.
#
# Test build (local only):
#   ./scripts/build.sh --test [--fast]
#     Bake TestMode into the binary: bypasses HTTP auth and uses isolated
#     "-test" storage/runtime dirs. Never use for releases.
#
# CI:
#   CI=true ./scripts/build.sh
#     Always refresh release scripts and the version marker. Only build, tag,
#     and upload binaries when the latest CHANGELOG version is not already tagged.
#
# Mirrors: there is no build mode for mirrors. Signed release artifacts are
# portable — copy the release bucket byte-for-byte and install with
# APP_RELEASE_URL pointing at the copy; all cosign signatures stay valid.
# See docs/MIRRORING.md.
#
# Dependencies: go, zig (static musl cross-builds of the release binaries for
# both arches), gcc (non-fast builds only: go test -race), and curl. Release
# binaries are fully static so they run on any distro, including NixOS.
#
# NixOS: the downloaded tailwind standalone is dynamically linked and won't
# run; local builds prefer a tailwindcss found on PATH instead. Use the repo
# flake (`nix develop`) or `nix shell nixpkgs#tailwindcss_4` before building.
# Note nixpkgs may lag TAILWIND_VERSION slightly — fine for local dev, CI
# always uses the pinned standalone.

set -euo pipefail
umask 022

# Config --------------------------------------------------------------

APP_NAME="servo"
RELEASE_URL="https://cd.example.com/"
CONTACT_URL="https://github.com/Data-Corruption/servo"

# cosign keyless identity: only releases signed by this exact workflow on main
# verify. Derived in CI (the only mode that renders the install scripts) and
# baked in.
OIDC_ISSUER="https://token.actions.githubusercontent.com"
CERT_IDENTITY=""
DEFAULT_LOG_LEVEL="warn"

SERVICE="true"
SERVICE_DESC="Servo game server dashboard daemon"
SERVICE_ARGS="service run"
SERVICE_DEFAULT_PORT="8484"

# -----------------------------------------------------------------------------

TAILWIND_VERSION="${TAILWIND_VERSION:-v4.3.2}"
DAISYUI_VERSION="${DAISYUI_VERSION:-v5.6.10}"

OUT_DIR="out"
RELEASE_DIR="$OUT_DIR/release"
TOOLS_DIR="./tools" # downloaded build tools (gitignored)
JS_DIR="./internal/ui/assets/js"
CSS_DIR="./internal/ui/assets/css"
GO_MAIN_PATH="./cmd"

NO_CACHE='Cache-Control: no-store, max-age=0, must-revalidate' # unneeded with cache rule but just in case

MODE="local"
VERSION="vX.X.X" # dev/test version
SHOULD_BUILD_BINARIES=true
SHOULD_TAG_VERSION=false
FAST_LOCAL=false
TEST_MODE=false # baked into BuildInfo; only enabled by --test on local builds
HOST_GOARCH=""
BUILD_OUTS=()

# Helpers ---------------------------------------------------------------------

# run_step "success_msg" "fail_msg" command [args...]
# Runs a command, prints success or failure message, exits on failure.
run_step() {
  local success_msg="$1"
  local fail_msg="$2"
  shift 2
  local output
  if output="$("$@" 2>&1)"; then
    printf '🟢 %s\n' "$success_msg"
    [[ -n "${VERBOSE:-}" && -n "$output" ]] && printf '%s\n' "$output" || true
  else
    local status=$?
    printf '\n🔴 %s:\n' "$fail_msg"
    printf '%s\n' "$output"
    exit $status
  fi
}

# download_file "output_path" "url"
# Downloads a file, with status output.
download_file() {
  run_step "Downloaded $2" "Failed to download $2" curl -fsSL -o "$1" "$2"
}

# check_var "key" "expected"
# Verifies a build variable matches the expected value.
# Handles both string values ("key":"value") and non-string values (key:value or key:true).
check_var() {
  local key="$1"
  local expected="$2"
  local actual
  # Try string value first, then non-string (bool/number)
  actual=$(echo "$BUILD_VARS" | grep -oP "\"$key\":\"[^\"]*\"" | cut -d'"' -f4) || true
  if [[ -z "$actual" ]]; then
    actual=$(echo "$BUILD_VARS" | grep -oP "\"$key\":[^,}]+" | cut -d':' -f2)
  fi
  if [[ "$actual" != "$expected" ]]; then
    echo "🔴 Error: $key mismatch. Expected '$expected', got '$actual'"
    exit 1
  fi
}

# Stages ----------------------------------------------------------------------

dep_check() {
  # zig cross-compiles the static musl release binaries (both arches);
  # gcc is only needed by the host-toolchain go test -race run.
  local required_bins=(go zig sed awk sha256sum gzip)
  if [[ "$MODE" != "local" || "$FAST_LOCAL" != "true" ]]; then
    required_bins+=(gcc)
  fi
  if [[ "$MODE" == "ci" ]]; then
    required_bins+=(cosign)
  fi

  for bin in "${required_bins[@]}"; do
    if ! command -v "$bin" >/dev/null 2>&1; then
      printf "error: '$bin' is required but not installed or not in \$PATH\n" >&2
      exit 1
    fi
  done
}

clean_out_dir() {
  rm -rf "$OUT_DIR" && mkdir -p "$OUT_DIR"
  printf '🟢 Cleaned out directory\n'
}

parse_args() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --fast)
        FAST_LOCAL=true
        shift
        ;;
      --test)
        TEST_MODE=true
        shift
        ;;
      *)
        printf "error: unknown argument '%s'\n" "$1" >&2
        exit 1
        ;;
    esac
  done
}

detect_mode() {
  if [[ "${CI:-}" == "true" ]]; then
    MODE="ci"
    if [[ -z "${GITHUB_REPOSITORY:-}" ]]; then
      printf "🔴 GITHUB_REPOSITORY not set; cannot derive cosign identity\n" >&2
      exit 1
    fi
    CERT_IDENTITY="https://github.com/${GITHUB_REPOSITORY}/.github/workflows/release.yml@refs/heads/main"
  else
    MODE="local"
  fi
}

validate_mode_flags() {
  if [[ "$FAST_LOCAL" == "true" && "$MODE" != "local" ]]; then
    printf "error: --fast is only supported for local builds\n" >&2
    exit 1
  fi
  if [[ "$TEST_MODE" == "true" && "$MODE" != "local" ]]; then
    printf "error: --test is only supported for local builds (it bypasses auth)\n" >&2
    exit 1
  fi
}

detect_host_arch() {
  case "$(uname -m)" in
    x86_64|amd64)
      HOST_GOARCH="amd64"
      ;;
    aarch64|arm64)
      HOST_GOARCH="arm64"
      ;;
    *)
      printf "error: unsupported host architecture '%s'\n" "$(uname -m)" >&2
      exit 1
      ;;
  esac
}

require_distribution_config() {
  if [[ "$MODE" == "ci" ]]; then
    if ! command -v rclone >/dev/null 2>&1; then
      printf "error: 'rclone' is required but not installed or not in \$PATH\n" >&2
      exit 1
    fi
    if [[ -z "${R2_ACCESS_KEY_ID:-}" || -z "${R2_SECRET_ACCESS_KEY:-}" || -z "${R2_ACCOUNT_ID:-}" || -z "${R2_BUCKET:-}" ]]; then
      printf "🔴 Distribution not configured\n" >&2
      exit 1
    fi
  fi
}

resolve_version() {
  if [[ "$MODE" == "ci" ]]; then
    VERSION=$(sed -n 's/^## \[\(.*\)\] - .*/\1/p' CHANGELOG.md | head -n 1)
    if [[ -z "$VERSION" ]]; then
      printf "No version found in CHANGELOG.md\n"
      exit 0
    fi
  fi
}

configure_distribution() {
  if [[ "$MODE" == "ci" ]]; then
    export RCLONE_CONFIG_R2_TYPE=s3
    export RCLONE_CONFIG_R2_PROVIDER=Cloudflare
    export RCLONE_CONFIG_R2_ACCESS_KEY_ID="$R2_ACCESS_KEY_ID"
    export RCLONE_CONFIG_R2_SECRET_ACCESS_KEY="$R2_SECRET_ACCESS_KEY"
    export RCLONE_CONFIG_R2_ENDPOINT="https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com"
  fi
}

resolve_release_policy() {
  SHOULD_BUILD_BINARIES=true
  SHOULD_TAG_VERSION=false

  if [[ "$MODE" == "ci" ]]; then
    if git show-ref --verify --quiet "refs/tags/$VERSION"; then
      SHOULD_BUILD_BINARIES=false
    else
      SHOULD_TAG_VERSION=true
    fi
  fi
}

frontend_build() {
  mkdir -p "$TOOLS_DIR"
  # Download tools if missing (or requested in CI)
  [[ "$MODE" == "ci" && "${REFETCH_TOOLS:-false}" == "true" ]] && rm -f "$TOOLS_DIR/esbuild" "$TOOLS_DIR/tailwindcss" "$TOOLS_DIR/daisyui.mjs" "$TOOLS_DIR/daisyui-theme.mjs"
  [[ -f "$TOOLS_DIR/esbuild" ]] || (cd "$TOOLS_DIR" && curl -fsSL https://esbuild.github.io/dl/latest | sh)
  [[ -f "$TOOLS_DIR/daisyui.mjs" ]] || download_file "$TOOLS_DIR/daisyui.mjs" "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui.mjs"
  [[ -f "$TOOLS_DIR/daisyui-theme.mjs" ]] || download_file "$TOOLS_DIR/daisyui-theme.mjs" "https://github.com/saadeghi/daisyui/releases/download/${DAISYUI_VERSION}/daisyui-theme.mjs"

  # The tailwind standalone is dynamically linked (glibc, and the -musl variant
  # against musl's loader), so it can't run on NixOS. Local builds prefer a
  # tailwindcss from PATH (e.g. nixpkgs tailwindcss_4, or the flake dev shell);
  # CI always downloads the pinned standalone for reproducibility.
  local tailwind_bin="$TOOLS_DIR/tailwindcss"
  if [[ "$MODE" == "local" ]] && command -v tailwindcss >/dev/null 2>&1; then
    tailwind_bin="tailwindcss"
  elif [[ ! -f "$TOOLS_DIR/tailwindcss" ]]; then
    download_file "$TOOLS_DIR/tailwindcss" "https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64"
  fi

  [[ "$tailwind_bin" != "tailwindcss" ]] && chmod +x "$TOOLS_DIR/tailwindcss"
  chmod +x "$TOOLS_DIR/esbuild"
  run_step "Tailwind CSS built" "Tailwind CSS failed" "$tailwind_bin" -i "$CSS_DIR/input.css" -o "$CSS_DIR/output.css" --minify
  run_step "JavaScript bundled" "JavaScript bundling failed" "$TOOLS_DIR/esbuild" "$JS_DIR/src/main.js" --bundle --minify --outfile="$JS_DIR/output.js"
}

frontend_hash_assets() {
  local assets_dir="./internal/ui/assets"
  local manifest="$assets_dir/manifest.json"
  
  # Patterns to ignore (matched against relative path from assets/)
  local ignore_patterns=(
    "css/input.css"
    "js/src/*"
    "manifest.json"
  )
  
  is_ignored() {
    local file="$1"
    for pattern in "${ignore_patterns[@]}"; do
      # shellcheck disable=SC2053
      if [[ "$file" == $pattern ]]; then
        return 0
      fi
    done
    return 1
  }
  
  # Build manifest as JSON
  local first=true
  printf '{' > "$manifest"
  
  while IFS= read -r -d '' file; do
    # Get relative path from assets dir
    local rel_path="${file#$assets_dir/}"
    
    # Skip ignored files
    if is_ignored "$rel_path"; then
      continue
    fi
    
    # Compute hash (first 16 chars of SHA256)
    local hash
    hash=$(sha256sum "$file" | cut -c1-16)
    
    # Add comma before all but first entry
    if $first; then
      first=false
    else
      printf ','
    fi >> "$manifest"
    
    # Write JSON entry
    printf '"%s":"%s"' "$rel_path" "$hash" >> "$manifest"
  done < <(find "$assets_dir" -type f -print0 | sort -z)
  
  printf '}' >> "$manifest"
  
  printf '🟢 Generated asset manifest\n'
}

tests() {
  run_step "Tests passed" "Tests failed" go test -race ./...
}

# make_ldflags <testMode>
# Prints the -ldflags string for a build with the given testMode value.
make_ldflags() {
  local pkg="servo/internal/build"
  local ldflags="-X '${pkg}.name=$APP_NAME'"
  ldflags+=" -X '${pkg}.version=$VERSION'"
  ldflags+=" -X '${pkg}.contactURL=$CONTACT_URL'"
  ldflags+=" -X '${pkg}.defaultLogLevel=$DEFAULT_LOG_LEVEL'"
  ldflags+=" -X '${pkg}.serviceEnabled=$SERVICE'"
  ldflags+=" -X '${pkg}.serviceDesc=$SERVICE_DESC'"
  ldflags+=" -X '${pkg}.serviceArgs=$SERVICE_ARGS'"
  ldflags+=" -X '${pkg}.serviceDefaultPort=$SERVICE_DEFAULT_PORT'"
  ldflags+=" -X '${pkg}.certIdentity=$CERT_IDENTITY'"
  ldflags+=" -X '${pkg}.oidcIssuer=$OIDC_ISSUER'"
  ldflags+=" -X '${pkg}.testMode=$1'"
  printf '%s' "$ldflags"
}

go_build() {
  local ldflags
  ldflags=$(make_ldflags "$TEST_MODE")

  BUILD_OUTS=()
  VERIFY_BUILD_OUT="$OUT_DIR/linux-amd64"

  local targets=(amd64 arm64)
  if [[ "$MODE" == "local" && "$FAST_LOCAL" == "true" ]]; then
    targets=("$HOST_GOARCH")
    VERIFY_BUILD_OUT="$OUT_DIR/linux-$HOST_GOARCH"
  fi

  # Release binaries are fully static (musl via zig cc): they must run on any
  # distro, including NixOS which has no /lib64/ld-linux-*.so glibc loader.
  local target build_out cc
  for target in "${targets[@]}"; do
    build_out="$OUT_DIR/linux-$target"
    case "$target" in
      amd64) cc="zig cc -target x86_64-linux-musl" ;;
      arm64) cc="zig cc -target aarch64-linux-musl" ;;
    esac

    GOOS=linux GOARCH="$target" CC="$cc" CGO_ENABLED=1 go build -trimpath -buildvcs=false -tags osusergo,netgo -ldflags="$ldflags -linkmode external -extldflags -static" -o "$build_out" "$GO_MAIN_PATH"
    BUILD_OUTS+=("$build_out")
    printf "🟢 Built %s\n" "$build_out"
  done
}

verify_build() {
  # Release binaries must be fully static (see go_build). For static binaries
  # ldd prints "not a dynamic executable" (or "statically linked" on some
  # systems); any other output line means dynamic linking crept back in.
  if ldd "$VERIFY_BUILD_OUT" 2>&1 | grep -Eqv 'not a dynamic executable|statically linked'; then
    printf "🔴 Error: %s is dynamically linked:\n" "$VERIFY_BUILD_OUT"
    ldd "$VERIFY_BUILD_OUT" 2>&1 || true
    exit 1
  fi
  printf "🟢 Verified static linking\n"

  # Only verify the amd64 binary on the amd64 runner.
  BUILD_VARS=$("$VERIFY_BUILD_OUT" --build-vars)
  export BUILD_VARS

  check_var "name" "$APP_NAME"
  check_var "version" "$VERSION"
  check_var "contactURL" "$CONTACT_URL"
  check_var "defaultLogLevel" "$DEFAULT_LOG_LEVEL"
  check_var "serviceEnabled" "$SERVICE"
  check_var "serviceDesc" "$SERVICE_DESC"
  check_var "serviceArgs" "$SERVICE_ARGS"
  check_var "serviceDefaultPort" "$SERVICE_DEFAULT_PORT"
  check_var "testMode" "$TEST_MODE"

  printf "🟢 Build variables verified\n"
}

package_installers() {
  mkdir -p "$RELEASE_DIR"

  sed -e "s|<APP_NAME>|$APP_NAME|g" \
      -e "s|<RELEASE_URL>|$RELEASE_URL|g" \
      -e "s|<SERVICE>|$SERVICE|g" \
      -e "s|<SERVICE_DESC>|$SERVICE_DESC|g" \
      -e "s|<SERVICE_ARGS>|$SERVICE_ARGS|g" \
      -e "s|<CERT_IDENTITY>|$CERT_IDENTITY|g" \
      -e "s|<OIDC_ISSUER>|$OIDC_ISSUER|g" \
      "./scripts/install.sh" > "$RELEASE_DIR/install.sh"
  printf "🟢 Processed install.sh\n"

  sed -e "s|<APP_NAME>|$APP_NAME|g" \
      -e "s|<RELEASE_URL>|$RELEASE_URL|g" \
      -e "s|<SERVICE>|\$$SERVICE|g" \
      "./scripts/install.ps1" > "$RELEASE_DIR/install.ps1"
  printf "🟢 Processed install.ps1\n"
}

package_binaries() {
  mkdir -p "$RELEASE_DIR"

  local build_out gzip_out
  for build_out in "${BUILD_OUTS[@]}"; do
    gzip_out="$RELEASE_DIR/$(basename "$build_out").gz"
    gzip -c -n -- "$build_out" > "$gzip_out"
    printf "🟢 Gzipped %s\n" "$build_out"
  done
}

write_release_version() {
  mkdir -p "$RELEASE_DIR"
  echo "$VERSION" > "$RELEASE_DIR/version"
  printf "🟢 Release packaged in %s\n" "$RELEASE_DIR"
}

# One file listing the sha256 of every release artifact (gzipped binaries +
# version marker). install.sh verifies its cosign signature once, then plain
# sha256-matches each artifact against it.
generate_checksums() {
  (
    cd "$RELEASE_DIR" || exit 1
    sha256sum linux-*.gz version > checksums.txt
  )
  printf "🟢 Generated checksums.txt\n"
}

# Keyless cosign signing (CI only): the workflow's OIDC identity is exchanged
# for a short-lived cert, and each bundle carries signature + cert + Rekor
# proof. The install scripts are signed every run (they are re-rendered every
# run); checksums.txt only when binaries were rebuilt — on an already-tagged
# version the previously uploaded signed checksums.txt remains valid and is
# not re-uploaded.
sign_release() {
  if $SHOULD_BUILD_BINARIES; then
    run_step "Signed checksums.txt" "Failed to sign checksums.txt" \
      cosign sign-blob --yes --bundle "$RELEASE_DIR/checksums.txt.cosign.bundle" "$RELEASE_DIR/checksums.txt"
  fi
  run_step "Signed install.sh" "Failed to sign install.sh" \
    cosign sign-blob --yes --bundle "$RELEASE_DIR/install.sh.cosign.bundle" "$RELEASE_DIR/install.sh"
  run_step "Signed install.ps1" "Failed to sign install.ps1" \
    cosign sign-blob --yes --bundle "$RELEASE_DIR/install.ps1.cosign.bundle" "$RELEASE_DIR/install.ps1"
}

distribute() {
  if $SHOULD_TAG_VERSION; then
    # GIT_TERMINAL_PROMPT=0 ensures failure instead of hang if auth fails
    run_step "Tagged $VERSION" "Failed to tag $VERSION" git tag "$VERSION"
    run_step "Pushed $VERSION" "Failed to push $VERSION" env GIT_TERMINAL_PROMPT=0 git push origin "$VERSION"
  fi

  local f
  for f in "$RELEASE_DIR"/*; do
    run_step "Uploaded $(basename "$f")" "Failed to upload $(basename "$f")" rclone copyto "$f" "r2:$R2_BUCKET/$(basename "$f")" --header-upload "$NO_CACHE" --s3-env-auth --s3-no-check-bucket
  done
}

# Main ------------------------------------------------------------------------

main() {
  clean_out_dir
  parse_args "$@"
  detect_mode
  validate_mode_flags
  detect_host_arch
  dep_check
  require_distribution_config
  resolve_version
  configure_distribution
  resolve_release_policy

  if $SHOULD_BUILD_BINARIES; then
    frontend_build
    frontend_hash_assets
    if [[ "$FAST_LOCAL" == "true" ]]; then
      printf "🟢 Skipping tests in fast local mode\n"
    else
      tests
    fi
    go_build
    verify_build
  elif [[ "$MODE" == "ci" ]]; then
    printf "🟢 Skipping binary build for tagged version\n"
  fi

  if [[ "$MODE" == "ci" ]]; then
    package_installers
    if $SHOULD_BUILD_BINARIES; then
      package_binaries
    fi
    write_release_version
    if $SHOULD_BUILD_BINARIES; then
      generate_checksums
    fi
    sign_release
    distribute
  fi
}

main "$@"
