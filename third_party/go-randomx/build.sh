#!/usr/bin/env bash
# Builds lib/librandomx.a from tevador/RandomX v1.2.1 for the host platform.
set -euo pipefail
TAG="${1:-v1.2.1}"
if [ ! -d RandomX ]; then
  git clone https://github.com/tevador/RandomX RandomX
fi
cd RandomX
git fetch --tags
git checkout "$TAG"
mkdir -p build && cd build
cmake -DARCH=native ..
make -j"$(sysctl -n hw.ncpu 2>/dev/null || nproc)"
mkdir -p ../../lib
cp librandomx.a ../../lib/
echo "lib/librandomx.a built from RandomX $TAG"
