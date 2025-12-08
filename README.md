# HyperSniper Indexer

High-speed TON jetton minter indexer aimed at 1–2s detection latency. This repository contains the standalone indexer used by HyperSniper Bot to stream new jetton minter events to downstream consumers.

## Quick start

- Prereqs: Go 1.23+, Docker, docker-compose.
- Configure `config.yaml` (network, Redis, Postgres DSN, Telegram token/chat, webhook).
- Local run: `go run ./cmd/indexer --network=mainnet`
- Docker compose: `cd docker && docker-compose up --build`
- Detailed Russian guide: `ИНСТРУКЦИЯ_ПО_ЗАПУСКУ.md`.

