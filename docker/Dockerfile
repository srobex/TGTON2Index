FROM golang:1.23 AS builder
WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/indexer ./cmd/indexer

FROM gcr.io/distroless/base-debian12
COPY --from=builder /out/indexer /indexer
COPY config.yaml /config/config.yaml
ENV CONFIG_PATH=/config/config.yaml
ENTRYPOINT ["/indexer"]

