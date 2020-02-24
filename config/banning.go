package config

type BanningOptions struct {
	Time           int     `json:"time"`
	InvalidPercent float64 `json:"invalidPercent"`
	CheckThreshold uint64  `json:"checkThreshold"`
	PurgeInterval  int     `json:"purgeInterval"` // unit seconds
}
