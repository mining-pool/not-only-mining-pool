package config

import (
	"crypto/tls"
	"strconv"

	"github.com/go-redis/redis/v8"
)

type RedisOptions struct {
	// The network type, either tcp or unix.
	// Default is tcp.
	Network string `json:"network"`

	Host string `json:"host"`
	Port int    `json:"port"`

	Password string `json:"password"`
	DB       int    `json:"db"`

	TLS *TLSClientOptions `json:"tls"`
}

func (ro *RedisOptions) Addr() string {
	return ro.Host + ":" + strconv.Itoa(ro.Port)
}

func (ro *RedisOptions) ToRedisOptions() *redis.Options {
	var tlsConfig *tls.Config

	if ro.TLS != nil {
		tlsConfig = ro.TLS.ToTLSConfig()
	}

	return &redis.Options{
		Network:   ro.Network,
		Addr:      ro.Addr(),
		Password:  ro.Password,
		DB:        ro.DB,
		TLSConfig: tlsConfig,
	}
}
