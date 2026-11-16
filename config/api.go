package config

import (
	"net"
	"strconv"
)

type APIOptions struct {
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	TLS            *TLSServerOptions `json:"tls,omitempty"`
	Admin          *AdminOptions     `json:"admin,omitempty"`
	AllowedOrigins []string          `json:"allowedOrigins,omitempty"`
	// HashesPerDifficulty overrides the work one unit of share difficulty
	// represents when reporting hashrates. Leave unset to use the engine's
	// (or, for GBT coins, the algorithm multiplier's) own definition.
	HashesPerDifficulty float64 `json:"hashesPerDifficulty,omitempty"`
}

func (api *APIOptions) Addr() string {
	return net.JoinHostPort(api.Host, strconv.Itoa(api.Port))
}

type AdminOptions struct {
	Username          string `json:"username"`
	PasswordHashFile  string `json:"passwordHashFile"`
	SessionTTLSeconds int    `json:"sessionTTLSeconds,omitempty"`
	// Local development only: HTTP sessions require both a loopback peer and host.
	AllowInsecureHTTP bool `json:"allowInsecureHTTP,omitempty"`
	// "strict" (default) for same-site deployments; "none" requires HTTPS.
	CookieSameSite string `json:"cookieSameSite,omitempty"`
}
