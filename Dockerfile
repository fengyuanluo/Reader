FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/reader ./cmd/reader

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S reader \
    && adduser -S -D -H -G reader reader \
    && mkdir -p /data \
    && chown reader:reader /data

COPY --from=build /out/reader /usr/local/bin/reader

ENV READER_ADDR=:8080 \
    READER_DATA_DIR=/data \
    READER_SQLITE_DSN=/data/reader.db \
    TMPDIR=/tmp

USER reader
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/reader", "serve"]
