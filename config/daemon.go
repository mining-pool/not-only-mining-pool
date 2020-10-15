package config

import (
	"strconv"
)

type DaemonOptions struct {
	Host     string            `json:"host"`
	Port     int               `json:"port"`
	User     string            `json:"user"`
	Password string            `json:"password"`
	TLS      *TLSClientOptions `json:"tls"`
}

func (d *DaemonOptions) String() string {
	return d.User + ":" + d.Password + "@" + d.Host + strconv.FormatInt(int64(d.Port), 10)
}

func (d *DaemonOptions) URL() string {
	if d.TLS != nil {
		return "https://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
	}

	return "http://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
}
