package jobs

import "crypto/rand"

type ExtraNonce1Generator struct {
	Size int
}

func NewExtraNonce1Generator() *ExtraNonce1Generator {
	return &ExtraNonce1Generator{
		Size: 4,
	}
}

func (eng *ExtraNonce1Generator) GetExtraNonce1() []byte {
	extraNonce := make([]byte, eng.Size)
	_, _ = rand.Read(extraNonce)

	return extraNonce
}
