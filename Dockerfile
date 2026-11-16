# Build stage: a static binary (no cgo), cross-compiled for the target platform
# so that a multi-arch buildx build needs no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS
ARG TARGETARCH
# Set by the release workflow (e.g. 3.0.0); reported by /health and -version.
ARG VERSION=3.0.0-dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X github.com/MedAziz11/Uptime-Kuma-Web-API/v3/internal/version.Version=${VERSION}" \
      -o /out/uptime-kuma-api ./cmd/uptime-kuma-api && \
    mkdir -p /out/root/db


# Runtime stage: no shell, no package manager; CA certificates and time zones only.
FROM gcr.io/distroless/static-debian12

COPY --from=build /out/uptime-kuma-api /uptime-kuma-api
# uid 100 / gid 101 are appuser:appgroup of the 2.0 (and 1.x) image: a /db volume
# created by 2.0 is read and written as it is, and a new volume gets the same owner.
COPY --from=build --chown=100:101 /out/root/ /

ENV DB_PATH=/db/db.sqlite3
USER 100:101
EXPOSE 8000
VOLUME /db

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/uptime-kuma-api", "healthcheck"]

ENTRYPOINT ["/uptime-kuma-api"]
