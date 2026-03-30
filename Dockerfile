FROM golang:1.23.2-alpine3.20 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
ADD cmd/ ./cmd
ADD internal ./internal
RUN mkdir ./bin
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./bin/hyperliquid-exporter ./cmd/hyperliquid-exporter

FROM ubuntu:24.04
WORKDIR /app
COPY --from=builder /app/bin/hyperliquid-exporter /bin/hyperliquid-exporter

ENV NODE_HOME="/hl/"
ENV BINARY_HOME="/bin"

RUN apt-get update && apt-get install -y ca-certificates curl wget

EXPOSE 8086
ENTRYPOINT ["/bin/hyperliquid-exporter"]
