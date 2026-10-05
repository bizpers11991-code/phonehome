# syntax=docker/dockerfile:1
# Multi-arch: docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 .
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARM="${TARGETVARIANT#v}" CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/phonehome ./cmd/phonehome \
 && mkdir -p /out/data

# distroless/static ships CA certificates and tzdata and runs as nonroot (65532).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/phonehome /usr/local/bin/phonehome
COPY --from=build --chown=65532:65532 /out/data /data
# GOMEMLIMIT keeps the 30-day report near 128 MiB on a Pi (docs/performance.md);
# override it with -e GOMEMLIMIT=... (e.g. "off" on a big machine).
ENV PHONEHOME_DB=/data/phonehome.db \
    PHONEHOME_LISTEN=:8099 \
    GOMEMLIMIT=128MiB
EXPOSE 8099
VOLUME /data
ENTRYPOINT ["/usr/local/bin/phonehome"]
# Without /config/phonehome.yaml, phonehome falls back to defaults and
# auto-detects Pi-hole / AdGuard files mounted at their usual paths.
CMD ["serve", "--config", "/config/phonehome.yaml"]
