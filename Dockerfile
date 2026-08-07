# Two stages, no QEMU: the Go stage runs on $BUILDPLATFORM with
# GOARCH=$TARGETARCH, so the arm64 image is cross-compiled at native speed.
# This is a concrete reason for the language choice, not a rationalisation
# of it — emulated arm64 builds take tens of minutes and time out.
#
# There is no Node stage. The UI is a single embedded HTML file with no
# build step; DESIGN D2 records that the SPA choice was close and that
# "the seam is the API: if this flips, nothing else in the design changes".

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO_ENABLED=0 is not optional: it is what makes the binary static, what
# makes the cross-compile work without a toolchain per architecture, and
# what the pure-Go SQLite driver was chosen to permit.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
      -o /out/orphanarr ./cmd/orphanarr


FROM alpine:3.20

# su-exec for the PUID/PGID path; tzdata so scheduled scans respect TZ;
# ca-certificates because a client may be behind HTTPS.
RUN apk add --no-cache ca-certificates su-exec tzdata && \
    addgroup -g 1000 orphanarr && \
    adduser -D -u 1000 -G orphanarr orphanarr

COPY --from=build /out/orphanarr /usr/local/bin/orphanarr
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

VOLUME ["/config"]
EXPOSE 8790

ENV ORPHANARR__CONFIG_DIR=/config \
    ORPHANARR__ADDR=:8790 \
    PUID=1000 \
    PGID=1000 \
    UMASK=002

# The health endpoint is deliberately unauthenticated so this works without
# baking an API key into the image.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O- http://127.0.0.1:8790/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["orphanarr"]

LABEL org.opencontainers.image.title="Orphanarr" \
      org.opencontainers.image.description="Files uncategorised completed downloads into media libraries" \
      org.opencontainers.image.source="https://github.com/StevenGann/Orphanarr" \
      org.opencontainers.image.licenses="MIT"
