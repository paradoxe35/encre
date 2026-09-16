#!/usr/bin/env bash
# Build the Rust FFI static library. Usage: build-rust-ffi.sh [target-triple]
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root/rust-ffi"

target="${1:-}"
cargo_args=(build --release)

# TRANSCRIBE_USE_SYSTEM_BLAS=OFF: the decoder would otherwise call cblas when a
# BLAS is found at build time, which the final cgo link never provides.
export TRANSCRIBE_CMAKE_ARGS="${TRANSCRIBE_CMAKE_ARGS:--DTRANSCRIBE_USE_SYSTEM_BLAS=OFF}"
if [ -n "$target" ]; then
  cargo_args+=(--target "$target")
  out_dir="target/$target/release"
else
  out_dir="target/release"
fi

# ggml clears CMAKE_STATIC_LIBRARY_PREFIX on WIN32, so it installs ggml.a while
# its link manifest says "ggml" and rustc looks for libggml.a.
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
