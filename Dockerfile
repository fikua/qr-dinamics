# syntax=docker/dockerfile:1
# One image, two binaries: /panel and /redirect. Pick one with the entrypoint.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/panel ./cmd/panel \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/redirect ./cmd/redirect \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/healthcheck ./cmd/healthcheck \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/panel /out/redirect /out/healthcheck /
# Writable data dir for the redirect snapshot (the image runs as nonroot).
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot
# GET /health of whichever binary the compose file runs (OBS-17, DEP-10).
# Traefik routes only to healthy containers: --start-interval makes a new one
# healthy seconds after it is ready instead of after the first 30 s interval.
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s --start-interval=2s --retries=3 CMD ["/healthcheck"]
ENTRYPOINT ["/panel"]
