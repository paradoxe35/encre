#!/usr/bin/env bash
# Build the Rust FFI static library, or with --test run its tests the same way.
# Usage: build-rust-ffi.sh [--test] [target-triple]
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/rust-ffi"

command=build
if [ "${1:-}" = "--test" ]; then
  command=test
  shift
fi
target="${1:-}"
cargo_args=("$command" --release)
if [ -n "$target" ]; then
  cargo_args+=(--target "$target")
  out_dir="target/$target/release"
else
  out_dir="target/release"
fi

# ggml clears the static lib prefix on WIN32 and installs ggml.a, but rustc looks for libggml.a.
normalize_native_archives() {
  shopt -s nullglob
  local archive name dir
  for archive in "$out_dir"/build/transcribe-cpp-sys-*/out/lib/*.a; do
    name="$(basename "$archive")"
    dir="$(dirname "$archive")"
    case "$name" in
      lib*) continue ;;
    esac
    cp -f "$archive" "$dir/lib$name"
    echo "  aliased $name -> lib$name"
  done
}

if cargo "${cargo_args[@]}"; then
  exit 0
fi

# The retry reuses the cached build-script output, so nothing native is rebuilt.
echo "Rust build failed; normalizing native archive names and retrying..."
normalize_native_archives
cargo "${cargo_args[@]}"
