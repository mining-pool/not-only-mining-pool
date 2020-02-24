package storage

import (
	"github.com/go-redis/redis/v7"
	"github.com/mining-pool/go-pool-server/config"
)

type DB struct {
	*redis.Client
}

func NewDB(options config.RedisOptions) *DB {
	return &DB{
		redis.NewClient(options.ToRedisOptions()),
	}
}
