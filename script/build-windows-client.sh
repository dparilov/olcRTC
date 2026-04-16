#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
BUILD_DIR="$PROJECT_ROOT/build/windows-export"
GOCACHE_DIR="$PROJECT_ROOT/build/.gocache"
TOOLCHAIN_CANDIDATE="${GOPATH:-$HOME/go}/pkg/mod/golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64/bin/go"

mkdir -p "$BUILD_DIR" "$GOCACHE_DIR"

if [[ -x "$TOOLCHAIN_CANDIDATE" ]]; then
  GO_BIN="$TOOLCHAIN_CANDIDATE"
else
  GO_BIN="go"
fi

GO_COMMON_ENV=(
  "GOCACHE=$GOCACHE_DIR"
  "GOOS=windows"
  "GOARCH=amd64"
)

run_attempt() {
  local name="$1"
  local cgo_enabled="$2"
  local output_name="$3"
  local stdout_file="$BUILD_DIR/${name}.stdout"
  local stderr_file="$BUILD_DIR/${name}.stderr"
  local exit_file="$BUILD_DIR/${name}.exit"

  rm -f "$stdout_file" "$stderr_file" "$exit_file" "$BUILD_DIR/$output_name"

  set +e
  (
    cd "$PROJECT_ROOT"
    env "${GO_COMMON_ENV[@]}" "CGO_ENABLED=$cgo_enabled" \
      "$GO_BIN" build -o "$BUILD_DIR/$output_name" ./cmd/windows-client \
      >"$stdout_file" 2>"$stderr_file"
  )
  local status=$?
  set -e
  printf '%s\n' "$status" >"$exit_file"
  return "$status"
}

write_summary() {
  local summary_file="$BUILD_DIR/summary.txt"
  local mingw=""
  local cgo0_status="missing"
  local cgo1_status="missing"
  local artifact_path="$BUILD_DIR/windows-client.exe"

  if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
    mingw=$(command -v x86_64-w64-mingw32-gcc)
  else
    mingw="not found"
  fi

  [[ -f "$BUILD_DIR/cgo-disabled.exit" ]] && cgo0_status=$(tr -d '\n' <"$BUILD_DIR/cgo-disabled.exit")
  [[ -f "$BUILD_DIR/cgo-enabled.exit" ]] && cgo1_status=$(tr -d '\n' <"$BUILD_DIR/cgo-enabled.exit")

  {
    echo "Windows client export summary"
    echo "go_binary=$GO_BIN"
    echo "mingw_cc=$mingw"
    echo "cgo_disabled_exit=$cgo0_status"
    echo "cgo_enabled_exit=$cgo1_status"
    if [[ -f "$artifact_path" ]]; then
      echo "artifact=$artifact_path"
      echo "result=success"
    else
      echo "artifact=not produced"
      echo "result=blocked"
      echo "blocker_1=Fyne's desktop GL path does not cross-build for windows/amd64 with CGO disabled."
      echo "blocker_2=A MinGW-w64 cross-compiler is required for CGO-enabled windows builds from Linux."
      echo "command_cgo_disabled=env ${GO_COMMON_ENV[*]} CGO_ENABLED=0 $GO_BIN build -o $BUILD_DIR/windows-client.exe ./cmd/windows-client"
      echo "command_cgo_enabled=env ${GO_COMMON_ENV[*]} CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc CXX=x86_64-w64-mingw32-g++ $GO_BIN build -o $BUILD_DIR/windows-client.exe ./cmd/windows-client"
    fi
  } >"$summary_file"
}

echo "==> Attempting windows/amd64 build with CGO disabled"
if run_attempt "cgo-disabled" "0" "windows-client.exe"; then
  write_summary
  echo "Windows executable produced at $BUILD_DIR/windows-client.exe"
  exit 0
fi

echo "==> CGO-disabled build failed; retrying with CGO enabled"
if run_attempt "cgo-enabled" "1" "windows-client.exe"; then
  write_summary
  echo "Windows executable produced at $BUILD_DIR/windows-client.exe"
  exit 0
fi

write_summary
echo "Windows executable was not produced. See $BUILD_DIR/summary.txt"
exit 1
