# Reproducible end-to-end test image: bundles the Go toolchain, redis, and the
# coin daemons (regtest/simnet), then runs the pool + e2e miners against real
# nodes for every coin — GBT (BTC/LTC/DASH/GRS/MONA/VTC) and engine coins
# (RVN/kawpow, ETC/ethash, XMR/randomx, KAS/kaspa).
#
#   docker build -t nomp-e2e -f scripts/e2e/Dockerfile .
#   docker run --rm nomp-e2e                 # every coin
#   docker run --rm nomp-e2e BTC GRS XMR     # subset
#
# GitHub's runners are amd64, where every bundled daemon publishes a linux
# binary. On arm64 a few engine daemons (kaspad, core-geth) have no release
# asset; those coins simply skip (their runner exits "daemon not installed").
FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      redis-server ca-certificates curl jq python3 unzip xxd bzip2 \
      cmake build-essential git \
    && rm -rf /var/lib/apt/lists/*

# --- coin daemons -----------------------------------------------------------
# One extractor for tar.gz / tar.bz2 / zip; copies the named binaries out of the
# extracted tree by name. Engine daemons are best-effort (|| true) so a stale
# URL or a missing per-arch asset never breaks the whole image — the affected
# coin just skips at runtime.
RUN set -eux; \
    dpkgarch="$(dpkg --print-architecture)"; \
    case "$dpkgarch" in amd64) A=x86_64; MON=x64; RK=1;; arm64) A=aarch64; MON=armv8; RK=0;; *) A=x86_64; MON=x64; RK=1;; esac; \
    dl() { url="$1"; shift; f=/tmp/dl; rm -rf /tmp/x; mkdir -p /tmp/x; \
           curl -fsSL -o "$f" "$url"; \
           case "$url" in \
             *.zip)      unzip -q "$f" -d /tmp/x;; \
             *.tar.bz2)  tar xjf "$f" -C /tmp/x;; \
             *)          tar xzf "$f" -C /tmp/x;; \
           esac; \
           for b in "$@"; do p="$(find /tmp/x -type f -name "$b" | head -1)"; \
             [ -n "$p" ] && install -m755 "$p" /usr/local/bin/"$b"; done; \
           rm -rf "$f" /tmp/x; }; \
    # --- GBT coins ---
    dl "https://bitcoincore.org/bin/bitcoin-core-27.0/bitcoin-27.0-${A}-linux-gnu.tar.gz" bitcoind bitcoin-cli; \
    dl "https://github.com/Groestlcoin/groestlcoin/releases/download/v31.0/groestlcoin-31.0-${A}-linux-gnu.tar.gz" groestlcoind groestlcoin-cli; \
    dl "https://github.com/monacoinproject/monacoin/releases/download/v0.20.4/monacoin-0.20.4-${A}-linux-gnu.tar.gz" monacoind monacoin-cli; \
    dl "https://github.com/vertcoin-project/vertcoin-core/releases/download/v23.2/vertcoin-23.2-${A}-linux-gnu.tar.gz" vertcoind vertcoin-cli; \
    dl "https://github.com/dashpay/dash/releases/download/v23.1.8/dashcore-23.1.8-${A}-linux-gnu.tar.gz" dashd dash-cli || true; \
    if [ "$A" = x86_64 ]; then \
      dl "https://download.litecoin.org/litecoin-0.21.4/linux/litecoin-0.21.4-x86_64-linux-gnu.tar.gz" litecoind litecoin-cli; \
    fi; \
    # --- engine coins (best-effort) ---
    dl "https://github.com/RavenProject/Ravencoin/releases/download/v4.6.2snapshot/raven-4.7.0-0b91b62a2-${A}-linux-gnu.tar.gz" ravend raven-cli || true; \
    dl "https://downloads.getmonero.org/cli/monero-linux-${MON}-v0.18.3.4.tar.bz2" monerod monero-wallet-rpc || true; \
    if [ "$RK" = 1 ]; then \
      dl "https://github.com/kaspanet/kaspad/releases/download/v0.12.13/kaspad-v0.12.13-linux.zip" kaspad kaspawallet || true; \
      dl "https://github.com/etclabscore/core-geth/releases/download/v1.12.19/core-geth-linux-v1.12.19.zip" geth || true; \
    fi; \
    bitcoind --version | head -1

WORKDIR /src
# cache go deps
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Build the RandomX static lib for THIS container's arch (the committed lib is
# built for the developer's host). Needed by the -tags randomx pool + miner.
RUN cd third_party/go-randomx && ./build.sh
# pre-build the default pool + generic miner so `docker run` starts fast; the
# per-coin runners rebuild with the right -tags at test time.
RUN CGO_ENABLED=1 go build -o /usr/local/bin/nomp-pool . \
 && go build -o /usr/local/bin/e2eminer ./tools/e2eminer/

ENV E2E_WORK=/tmp/nomp-e2e
ENTRYPOINT ["bash", "scripts/e2e/run-all.sh"]
