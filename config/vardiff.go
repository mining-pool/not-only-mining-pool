package config

type VarDiffOptions struct {
	MinDiff         float64 `json:"minDiff"`
	MaxDiff         float64 `json:"maxDiff"`
	TargetTime      int64   `json:"targetTime"`
	RetargetTime    int64   `json:"retargetTime"`
	VariancePercent float64 `json:"variancePercent"`
	X2Mode          bool    `json:"x2mode"`
}
