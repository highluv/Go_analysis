FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /acgd ./cmd/acgd

FROM alpine:3.20
COPY --from=build /acgd /usr/local/bin/acgd
EXPOSE 8080
ENTRYPOINT ["acgd"]