package config

type AlgorithmOptions struct {
	Name               string `json:"name"`
	Multiplier         int    `json:"multiplier"`
	SHA256dBlockHasher bool   `json:"sha256dBlockHasher"`
}
