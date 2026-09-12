#!/usr/bin/env bash
set -euo pipefail
# npm's 0.3.0 tarball omits pkg/. Build the missing bindings from the exact
# upstream release source instead of downloading an unpinned binary.
wallet_dir="$(cd "$(dirname "$0")" && pwd)"
build_dir="$(mktemp -d)"
trap 'rm -rf "$build_dir"' EXIT
git clone --quiet https://github.com/Quantus-Network/quantus-wasm.git "$build_dir/source"
git -C "$build_dir/source" checkout --quiet 539e3474e14d0dd5c2039c95e77e996e9d12bf93
rustup target add wasm32-unknown-unknown
cargo build --manifest-path "$build_dir/source/Cargo.toml" --locked --release --target wasm32-unknown-unknown
if command -v wasm-bindgen >/dev/null && [[ "$(wasm-bindgen --version)" == "wasm-bindgen 0.2.125" ]]; then
  bindgen_bin="$(command -v wasm-bindgen)"
else
  cargo install wasm-bindgen-cli --version 0.2.125 --locked --root "$build_dir/tools"
  bindgen_bin="$build_dir/tools/bin/wasm-bindgen"
fi
"$bindgen_bin" "$build_dir/source/target/wasm32-unknown-unknown/release/quantus_wasm.wasm" \
  --target nodejs --out-dir "$wallet_dir/node_modules/@quantus-network/wasm/pkg"
node --input-type=module -e "import('$wallet_dir/node_modules/@quantus-network/wasm/dist/index.js').then(m => { if (m.account(new Uint8Array(32)).address !== 'qzk1Nxai3dZD9Cn5kwGcgL6mKxsfxwqdis7kDQJ52aJS2vSn7') process.exit(1); })"
