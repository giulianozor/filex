# Stage 1: build
FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -ldflags "-X main.Version=$(git describe --tags --always 2>/dev/null || echo dev)" \
    -o /filex ./cmd/filex

# Stage 2: minimal runtime image
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata ffmpeg

COPY --from=builder /filex /usr/local/bin/filex

RUN mkdir -p /data /etc/filex

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/api/config || exit 1

ENTRYPOINT ["filex"]
CMD ["-config", "/etc/filex/config.yaml"]
