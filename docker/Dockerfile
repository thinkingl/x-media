FROM 172.16.240.100:5001/library/golang:1.25-bookworm AS backend-builder

ENV http_proxy=http://172.16.195.148:8118
ENV https_proxy=http://172.16.195.148:8118

RUN sed -i 's|http://deb.debian.org|http://172.16.240.100:3142|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends gcc libsqlite3-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY server/go.mod server/go.sum ./
RUN GOPROXY=https://goproxy.cn,direct go mod download

COPY server/ .
RUN CGO_ENABLED=1 GOOS=linux go build -o x-media-server ./cmd/main.go

FROM 172.16.240.100:5001/library/node:18-alpine AS frontend-builder

WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry https://registry.npmmirror.com
COPY web/ .
RUN npm run build

FROM 172.16.240.100:5001/library/debian:bookworm-slim

RUN sed -i 's|http://deb.debian.org|http://172.16.240.100:3142|g' /etc/apt/sources.list.d/debian.sources && \
    apt-get update && \
    apt-get install -y --no-install-recommends sqlite3 libsqlite3-0 ca-certificates tzdata ffmpeg && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=backend-builder /build/x-media-server .
COPY --from=frontend-builder /build/dist ./web/dist
COPY docker/config.yaml ./config.yaml

RUN mkdir -p /app/data /app/logs

EXPOSE 18090 8080 1935 5544

CMD ["./x-media-server", "-c", "config.yaml", "-s", "/app/web/dist"]
