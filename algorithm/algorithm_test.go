package algorithm

import (
	"encoding/hex"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"testing"
)

func TestHash(t *testing.T) {
	log.Println(MaxTargetTruncated)
}

func TestScryptHash(t *testing.T) {
	headerHex := "01000000f615f7ce3b4fc6b8f61e8f89aedb1d0852507650533a9e3b10b9bbcc30639f279fcaa86746e1ef52d3edb3c4ad8259920d509bd073605c9bf1d59983752a6b06b817bb4ea78e011d012d59d4"
	headerBytes, err := hex.DecodeString(headerHex)
	if err != nil {
		t.Log(err)
	}
	result := hex.EncodeToString(utils.ReverseBytes(ScryptHash(headerBytes)))
	if result != "0000000110c8357966576df46f3b802ca897deb7ad18b12f1c24ecff6386ebd9" {
		t.Log(result)
		t.Fail()
	}
}
