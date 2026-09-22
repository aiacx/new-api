FROM oven/bun:1.4.0@sha256:5ff609364c049b54eb0ff560ec96319729a972078ef2c755d758f0c6ef89c2d6 AS builder
ARG UPSTREAM_TAG=unknown

WORKDIR /build/web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY ./web ./
RUN DISABLE_ESLINT_PLUGIN='true' VITE_REACT_APP_VERSION=${UPSTREAM_TAG} bun run build

FROM golang:1.26.8-alpine@sha256:51a7c389a5ddaf82f527191a1e9bff9928655130a44e4975dd1d7e0acf59f1ae AS builder2
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
RUN go build -ldflags "-s -w -X github.com/QuantumNous/new-api/common.Version=${UPSTREAM_TAG} -X github.com/QuantumNous/new-api/common.PanstarSourceCommit=${BUILD_REVISION} -X github.com/QuantumNous/new-api/common.PanstarUpstreamTag=${UPSTREAM_TAG} -X github.com/QuantumNous/new-api/common.PanstarUpstreamCommit=${UPSTREAM_COMMIT} -X github.com/QuantumNous/new-api/common.PanstarSourceDirty=${BUILD_DIRTY}" -o new-api

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

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

RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -g 10001 -S newapi \
    && adduser -u 10001 -S -D -H -G newapi newapi \
    && mkdir -p /data /app/logs \
    && chown -R 10001:10001 /data /app

COPY --from=builder2 /build/new-api /
COPY LICENSE NOTICE THIRD-PARTY-LICENSES.md /licenses/
EXPOSE 3000
WORKDIR /data
USER 10001:10001
ENTRYPOINT ["/new-api"]
