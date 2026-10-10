FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/casamod-store ./cmd/casamod-store

FROM scratch
# The golang alpine image ships the CA bundle needed to reach GitHub over HTTPS.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/casamod-store /casamod-store
ENV CASAMOD_STORE_ADDR=:8418 \
    CASAMOD_STORE_DATA=/DATA/AppData/casamod-store \
    CASAMOD_DIR=/DATA/AppData/casamod \
    DOCKER_SOCKET=/var/run/docker.sock \
    GOMEMLIMIT=24MiB
EXPOSE 8418
ENTRYPOINT ["/casamod-store"]
