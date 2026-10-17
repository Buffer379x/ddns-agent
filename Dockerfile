FROM node:24.19.0-alpine3.24 AS frontend
WORKDIR /ui
COPY web/package.json web/package-lock.json ./
RUN npm ci --ignore-scripts
COPY web/ ./
RUN npm test && npm run build

FROM golang:1.27.2-alpine3.24 AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /ui/dist ./web/dist
RUN VERSION=$(cat VERSION) && CGO_ENABLED=1 go build \
    -trimpath -ldflags="-s -w -X main.version=${VERSION} -linkmode external -extldflags '-static'" \
    -tags 'sqlite_omit_load_extension netgo osusergo' -o /ddns-agent ./cmd/ddns-agent

FROM alpine:3.24.2
RUN apk add --no-cache ca-certificates tzdata su-exec
COPY --from=builder /ddns-agent /ddns-agent
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod 755 /docker-entrypoint.sh
ENV DDNS_DATA_DIR=/data DDNS_UID=65532 DDNS_GID=65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/ddns-agent", "--health"]
ENTRYPOINT ["/docker-entrypoint.sh"]
