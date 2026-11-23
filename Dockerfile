# JuggleIM (juggleim/im-server) for Railway.
#
# Upstream publishes no container image: its official Dockerfile compiles the
# server from source. This file does the same, from the v1.8.16 release
# tarball (checksum-verified), plus a small launcher (launcher/main.go) that
# wires the server to Railway: one public port, MySQL schema on first boot, a
# generated console password and the public WebSocket address for SDKs.

ARG JUGGLEIM_VERSION=1.8.16
ARG JUGGLEIM_SHA256=b782e6b1142e1ee576e3fcc189c1f4cf193625c3fe1a4df2799b3f7bb04f1336

# ---- Build stage ----
FROM golang:1.25.14-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS builder
ARG JUGGLEIM_VERSION
ARG JUGGLEIM_SHA256
WORKDIR /src
RUN wget -q -O /tmp/src.tgz "https://codeload.github.com/juggleim/im-server/tar.gz/refs/tags/v${JUGGLEIM_VERSION}" \
 && echo "${JUGGLEIM_SHA256}  /tmp/src.tgz" | sha256sum -c - \
 && tar xzf /tmp/src.tgz --strip-components=1 -C /src \
 && rm /tmp/src.tgz
RUN go mod download
COPY launcher/ /src/railway/
# Same build as upstream's Dockerfile, plus the Railway launcher in the same module.
RUN cd launcher \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/imserver main.go \
 && cd /src \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/jim-railway ./railway \
 && mkdir -p /out/sql /out/conf /out/logs /out/data \
 && cp sql/imserver.sql /out/sql/

# ---- Runtime stage ----
FROM alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f
ARG JUGGLEIM_VERSION
LABEL org.opencontainers.image.source="https://github.com/aalfath/juggleim-railway-template" \
      org.opencontainers.image.version="${JUGGLEIM_VERSION}"
# CA bundle and Go's time-zone database from the builder (no package installs).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /usr/local/go/lib/time/zoneinfo.zip /usr/local/go/lib/time/zoneinfo.zip
WORKDIR /opt
COPY --from=builder /out/ /opt/
ENV PORT=8080 JUGGLEIM_DATA_DIR=/opt/data ZONEINFO=/usr/local/go/lib/time/zoneinfo.zip
EXPOSE 8080
ENTRYPOINT ["/opt/jim-railway"]
