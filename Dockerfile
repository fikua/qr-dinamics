# syntax=docker/dockerfile:1
# One image, two binaries: /panel and /redirect. Pick one with the entrypoint.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/panel ./cmd/panel \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/redirect ./cmd/redirect \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/panel /out/redirect /
# Writable data dir for the redirect snapshot (the image runs as nonroot).
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot
ENTRYPOINT ["/panel"]
