# syntax=docker/dockerfile:1

FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notifier ./notifier/cmd/notifier

FROM golang:1.27 AS emulator-build
WORKDIR /src

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/emulator ./emulator/cmd/emulator

FROM debian:bookworm-slim AS emulator
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=emulator-build /out/emulator /app/emulator
USER 65532:65532
ENTRYPOINT ["/app/emulator"]

FROM debian:bookworm-slim AS notifier
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app/run
COPY --from=build /out/notifier /app/notifier
COPY notifier/configs/config.yaml notifier/configs/config.yaml
USER 65532:65532
ENTRYPOINT ["/app/notifier"]
