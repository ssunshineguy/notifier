# syntax=docker/dockerfile:1

FROM golang:1.27 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/notifier ./cmd/notifier

FROM gcr.io/distroless/static-debian12:nonroot AS notifier
WORKDIR /app
COPY --from=build /out/notifier /app/notifier
COPY configs/config.yaml /app/configs/config.yaml
ENTRYPOINT ["/app/notifier"]
