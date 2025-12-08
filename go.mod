module github.com/yourname/hyper-sniper-indexer

go 1.23

require (
	github.com/fatih/color v1.16.0
	github.com/jackc/pgx/v5 v5.5.4
	github.com/joho/godotenv v1.5.1
	github.com/pkg/errors v0.9.1
	github.com/redis/go-redis/v9 v9.5.1
	github.com/spf13/viper v1.18.2
	go.uber.org/zap v1.27.0
	github.com/ton-org/ton-indexer v0.1.0
)

replace github.com/ton-org/ton-indexer => github.com/ton-org/ton-indexer v0.1.0

