FROM golang:1.21-alpine AS backend-builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /build
COPY server/go.mod server/go.sum ./
RUN go mod download

COPY server/ .
RUN CGO_ENABLED=1 GOOS=linux go build -o x-media-server ./cmd/main.go

FROM node:18-alpine AS frontend-builder
WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM alpine:3.18
RUN apk add --no-cache sqlite-libs ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /build/x-media-server .
COPY --from=frontend-builder /build/dist ./web/dist
COPY docker/config.yaml ./config.yaml

RUN mkdir -p /app/data /app/logs

EXPOSE 8080 1935 5544

CMD ["./x-media-server", "-c", "config.yaml"]
