package config

type PortOptions struct {
	Diff    float64           `json:"diff"`
	VarDiff *VarDiffOptions   `json:"varDiff"`
	TLS     *TLSServerOptions `json:"tls"`
}
