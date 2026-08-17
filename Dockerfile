# syntax=docker/dockerfile:1

# Build stage: compile a fully static binary for the target platform. The
# builder runs on the build platform and cross-compiles via GOARCH so a single
# build can target amd64 and arm64.
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/patrol ./cmd/server

# Runtime stage: minimal base, only the binary and config. No toolchain, no
# source, no build cache.
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S patrol && adduser -S patrol -G patrol
COPY --from=builder /out/patrol /usr/local/bin/patrol
COPY config.json /etc/patrol/config.json
USER patrol
EXPOSE 50253
ENTRYPOINT ["/usr/local/bin/patrol", "-config", "/etc/patrol/config.json"]
