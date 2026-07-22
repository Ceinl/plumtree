#!/usr/bin/env bash
set -euo pipefail

workspace_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
output_dir=${1:-"$workspace_root/dist"}

mkdir -p "$output_dir"
output_dir=$(cd "$output_dir" && pwd)

# Regenerate the server's hermetic build bundle so every release contains the
# SDK and TUI runtime from the exact source revision being built.
(
  cd "$workspace_root/control-plane"
  go generate ./internal/buildassets
)

targets=(
  linux/amd64
  linux/arm64
  darwin/amd64
  darwin/arm64
  windows/amd64
)

for target in "${targets[@]}"; do
  target_os=${target%/*}
  target_arch=${target#*/}
  extension=""
  if [[ "$target_os" == windows ]]; then
    extension=.exe
  fi

  pt_output="$output_dir/pt-$target_os-$target_arch$extension"
  server_output="$output_dir/plumtree-server-$target_os-$target_arch$extension"

  echo "==> build pt $target"
  (
    cd "$workspace_root/pt"
    GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w" -o "$pt_output" .
  )

  echo "==> build plumtree-server $target"
  (
    cd "$workspace_root/control-plane"
    GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
      go build -trimpath -ldflags="-s -w" -o "$server_output" ./cmd/control-plane
  )
done

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum pt-* plumtree-server-* > checksums.txt
  else
    shasum -a 256 pt-* plumtree-server-* > checksums.txt
  fi
)

echo "release assets written to $output_dir"
