FROM golang:1.23 AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /out/indexer ./cmd/indexer

FROM gcr.io/distroless/base-debian12
COPY --from=builder /out/indexer /indexer
ENTRYPOINT ["/indexer"]

