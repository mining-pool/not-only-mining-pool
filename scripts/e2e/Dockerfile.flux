# Isolated e2e image for the Flux/ZelHash (equihash 125,4) payout leg. Kept
# separate from the main Dockerfile because fluxd needs ~775MB of zk-SNARK params
# — bundling them into the main image would slow every e2e run. Built + run only
# by the opt-in `e2e-flux` CI job.
#
#   docker build -t nomp-e2e-flux -f scripts/e2e/Dockerfile.flux .
#   docker run --rm nomp-e2e-flux
FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      redis-server ca-certificates curl python3 lsof libgomp1 \
    && rm -rf /var/lib/apt/lists/*

# fluxd + flux-cli (amd64; the CI runner is amd64).
RUN set -eux; \
    curl -fsSL -o /tmp/flux.tar.gz \
      "https://github.com/RunOnFlux/fluxd/releases/download/v9.1.0/Flux-amd64-v9.1.0.tar.gz"; \
    mkdir -p /tmp/flux; tar xzf /tmp/flux.tar.gz -C /tmp/flux; \
    install -m755 /tmp/flux/fluxd    /usr/local/bin/fluxd; \
    install -m755 /tmp/flux/flux-cli /usr/local/bin/flux-cli; \
    rm -rf /tmp/flux /tmp/flux.tar.gz; \
    fluxd --version | head -1

# zk-SNARK proving params (sapling + sprout-groth16, ~775MB) into the default
# ~/.zcash-params zcashd/fluxd search path. The canonical z.cash CDN is more
# reliable for the 725MB sprout file than the runonflux mirror; force HTTP/1.1 and
# retry/resume so a dropped stream doesn't fail the whole build.
RUN set -eux; \
    d=/root/.zcash-params; mkdir -p "$d"; \
    base="https://download.z.cash/downloads"; \
    for f in sapling-spend.params sapling-output.params sprout-groth16.params; do \
      curl -fSL --http1.1 --retry 8 --retry-delay 5 --retry-all-errors -C - -o "$d/$f" "$base/$f"; \
    done; \
    ln -sf "$d" /root/.flux-params; \
    ls -la "$d"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The equihash engine is in the default build (no build tag).
RUN CGO_ENABLED=1 go build -o /usr/local/bin/nomp-pool ./cmd/nomp

ENV E2E_WORK=/tmp/nomp-e2e
ENTRYPOINT ["bash", "scripts/e2e/payment-flux.sh"]
