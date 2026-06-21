FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /acgd ./cmd/acgd

FROM alpine:3.20
RUN addgroup -S acg && adduser -S acg -G acg
COPY --from=build /acgd /usr/local/bin/acgd
USER acg

ENV ACG_HTTP_ADDR=":8080" \
    ACG_STORE="postgres" \
    ACG_BLOB="minio" \
    ACG_SOURCE="cluster" \
    ACG_POSTGRES_DSN="postgres://acg:acg-dev-password@postgres.acg-system:5432/acg?sslmode=disable" \
    ACG_MINIO_ENDPOINT="minio.acg-system:9000" \
    ACG_MINIO_ACCESS_KEY="acg-minio" \
    ACG_MINIO_SECRET_KEY="acg-minio-dev-password" \
    ACG_MINIO_BUCKET="acg-raw" \
    ACG_MINIO_USE_SSL="false"

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=5s \
  CMD wget -qO- http://localhost:8080/healthz || exit 1
ENTRYPOINT ["acgd"]