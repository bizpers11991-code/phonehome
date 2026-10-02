# syntax=docker/dockerfile:1
# Multi-arch: docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 .
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
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
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/phonehome /usr/local/bin/phonehome
COPY --from=build --chown=65532:65532 /out/data /data
ENV PHONEHOME_DB=/data/phonehome.db \
    PHONEHOME_LISTEN=:8099
EXPOSE 8099
VOLUME /data
ENTRYPOINT ["/usr/local/bin/phonehome"]
# Without /config/phonehome.yaml, phonehome falls back to defaults and
# auto-detects Pi-hole / AdGuard files mounted at their usual paths.
CMD ["serve", "--config", "/config/phonehome.yaml"]
