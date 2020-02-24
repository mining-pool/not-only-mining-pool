package config

import "strconv"

type DaemonOptions struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func (d *DaemonOptions) String() string {
	return d.User + ":" + d.Password + "@" + d.Host + strconv.FormatInt(int64(d.Port), 10)
}

func (d *DaemonOptions) URL() string {
	return "http://" + d.Host + ":" + strconv.FormatInt(int64(d.Port), 10)
}
