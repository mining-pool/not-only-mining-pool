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

	// ZMQ is an optional publisher endpoint (e.g. "tcp://127.0.0.1:28332") for
	// new-block notifications. Engines subscribe to it for event-driven work and
	// fall back to polling when it is empty. bitcoind: -zmqpubhashblock;
	// monerod: --zmq-pub.
	ZMQ string `json:"zmq"`
}

func (d *DaemonOptions) String() string {
	return d.User + "@" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
}

func (d *DaemonOptions) URL() string {
	if d.TLS != nil {
		return "https://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
	}

	return "http://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
}
