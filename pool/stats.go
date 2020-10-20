package pool

type Stats struct {
	Connections     int
	Difficulty      float64
	NetworkHashrate float64
	StratumPorts    []int
}

func NewStats() *Stats {
	return &Stats{
		Connections:     0,
		Difficulty:      0.0,
		NetworkHashrate: 0,
		StratumPorts:    []int{},
	}
}
