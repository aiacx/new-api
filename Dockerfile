FROM oven/bun:1.4.0@sha256:5ff609364c049b54eb0ff560ec96319729a972078ef2c755d758f0c6ef89c2d6 AS builder

WORKDIR /build/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY ./web ./
COPY ./VERSION /build/VERSION
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=$(cat /build/VERSION) bun run build

FROM golang:1.26.1-alpine@sha256:2389ebfa5b7f43eeafbd6be0c3700cc46690ef842ad962f6c5bd6be49ed82039 AS builder2
ARG BUILD_REVISION=unknown
ARG UPSTREAM_TAG=unknown
ARG UPSTREAM_COMMIT=unknown
ARG BUILD_DIRTY=unknown
ENV GO111MODULE=on CGO_ENABLED=0 GOWORK=off

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}
ENV GOEXPERIMENT=greenteagc

WORKDIR /build

ADD go.mod go.sum ./
# relaykit is a local submodule referenced via replace; its go.mod must be
# present for go mod download to resolve the main module graph.
ADD relaykit/go.mod ./relaykit/go.mod
RUN go mod download

COPY . .
COPY --from=builder /build/web/dist ./web/dist
RUN go build -ldflags "-s -w -X 'github.com/QuantumNous/new-api/common.Version=$(cat VERSION)' -X github.com/QuantumNous/new-api/common.PanstarSourceCommit=${BUILD_REVISION} -X github.com/QuantumNous/new-api/common.PanstarUpstreamTag=${UPSTREAM_TAG} -X github.com/QuantumNous/new-api/common.PanstarUpstreamCommit=${UPSTREAM_COMMIT} -X github.com/QuantumNous/new-api/common.PanstarSourceDirty=${BUILD_DIRTY}" -o new-api

FROM debian:bookworm-slim@sha256:f06537653ac770703bc45b4b113475bd402f451e85223f0f2837acbf89ab020a

ARG BUILD_REVISION=unknown
ARG UPSTREAM_TAG=unknown
ARG UPSTREAM_COMMIT=unknown
ARG BUILD_DIRTY=unknown
LABEL org.opencontainers.image.source="https://github.com/aiacx/new-api" \
      org.opencontainers.image.revision="${BUILD_REVISION}" \
      io.panstar.new-api.upstream-tag="${UPSTREAM_TAG}" \
      io.panstar.new-api.upstream-commit="${UPSTREAM_COMMIT}" \
      io.panstar.new-api.dirty="${BUILD_DIRTY}" \
      org.opencontainers.image.licenses="AGPL-3.0-only"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata libasan8 wget \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates \
    && groupadd --gid 10001 newapi \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin newapi \
    && mkdir -p /data /app/logs \
    && chown -R 10001:10001 /data /app

COPY --from=builder2 /build/new-api /
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/
EXPOSE 3000
WORKDIR /data
USER 10001:10001
ENTRYPOINT ["/new-api"]
