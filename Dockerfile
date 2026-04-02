FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build

COPY go.mod go.sum* ./
COPY . .

RUN go mod tidy

RUN VERSION=$(cat VERSION 2>/dev/null || echo "1.0.0") && \
    CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -linkmode external -extldflags '-static'" \
    -tags 'sqlite_omit_load_extension netgo osusergo' \
    -o ddns-agent \
    ./cmd/ddns-agent

# --- Runtime ---
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/ddns-agent /ddns-agent

EXPOSE 8080

VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/ddns-agent", "--health"]

ENTRYPOINT ["/ddns-agent"]
