package config

type PortOptions struct {
	// Protocol selects a Quantus miner transport: "quic" (default) or "stratum".
	Protocol string            `json:"protocol,omitempty"`
	Diff     float64           `json:"diff"`
	VarDiff  *VarDiffOptions   `json:"varDiff"`
	TLS      *TLSServerOptions `json:"tls"`
}
