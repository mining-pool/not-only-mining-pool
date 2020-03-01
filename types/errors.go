package types

var ErrToCodeMap = map[string]int{
	"job not found":                 20,
	"incorrect size of extranonce2": 21,
	"incorrect size of ntime":       22,
	"ntime out of range":            23,
	"incorrect size of nonce":       24,
	"duplicate share":               25,
	"low difficulty share":          26,
}

var CodeToErrMap = map[int]string{
	10: "you are banned by pool",
	20: "job not found",
	21: "incorrect size of extranonce2",
	22: "incorrect size of ntime",
	23: "ntime out of range",
	24: "incorrect size of nonce",
	25: "duplicate share",
	26: "low difficulty share",
}
