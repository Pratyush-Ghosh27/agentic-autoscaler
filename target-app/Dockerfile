# syntax=docker/dockerfile:1.7

FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags='-s -w' \
    -o /out/target-app ./cmd/target-app

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/target-app /target-app

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/target-app"]
