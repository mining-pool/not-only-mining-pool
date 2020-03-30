package config

import "strconv"

type APIOptions struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

func (api *APIOptions) Addr() string {
	return api.Host + ":" + strconv.FormatInt(int64(api.Port), 10)
}
