package transactions

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"github.com/node-standalone-pool/go-pool-server/daemonManager"
	"github.com/node-standalone-pool/go-pool-server/utils"
	"log"
	"testing"
)

func TestCreateGeneration(t *testing.T) {
	data := `
{
  "capabilities": [
    "proposal"
  ],
  "version": 536870912,
  "rules": [
    "csv",
    "segwit"
  ],
  "vbavailable": {
  },
  "vbrequired": 0,
  "previousblockhash": "b83b698bed0897ac94819041aec857d1a26a567bf7bf046d60849d5ccf24155e",
  "transactions": [
    {
      "data": "01000000012f8975c900f56662f35c317a0669fecc5fe0e1fb8ee53f4de72f1cb68c07e606010000008a473044022061a9ac17f269f3c69e18b5d67dfa6bf8b6a5a60eb7f9b0c992ffaeb66b5b88fb02202bb6fd7eb539302d97f4b8604bc822c91747e1cb365fecc91b37526b6b8c2c25014104fe67366f857106ee7b4cc48abb4dabd46302e12fe4140f4c933b92bd3ce75b1f4ae45055312f9a6c5ddc1f8d94d4f6d11e2a13372bcd6bfd651e48997b0f767effffffff02e8030000000000001976a914dffec839eba107e556d6c4f25f90765b3d10583288acbb60da04000000001976a914bdd83cf3ab8b7a57ff9b841752c1ae764f2a02ee88ac00000000",
      "txid": "f9b8b0bdd0dc38b2a707faf89acf064f543c3a88d39f54fb126cbd084ffb5ed9",
      "hash": "f9b8b0bdd0dc38b2a707faf89acf064f543c3a88d39f54fb126cbd084ffb5ed9",
      "depends": [
      ],
      "fee": 6450,
      "sigops": 8,
      "weight": 1028
    },
    {
      "data": "0200000001979a795a82096fc375487778939d9193bb284c58525e5df9c3a404c81c9220ef01000000d9004730440220086f0b09ded442c84e602520f5a8b38b41a1bc860fb595bd47834c20fa8db39402200a40cb86c15198302cabfd5c620c24fa6ac9ac5d946394e37dd3f9960b65a0e701473044022049bc0be153a4535196f73455bf82667956f2089019db4eeb57cb35649d8f69b202206ddd411917cb3e54f7b9a89c0693c971a6499eb6e421dce1d5fa9358300525d301475221025ad7eedea4c87b98463b8c7316c139f94c0e75fe4c849f42dab112479e1a1bb7210257591ace4d6a9fc94b8114cffd84df9bd0349c974a792580f7f5afb74f5ba94952ae0000000002102700000000000017a914b75a640760f2caae367c0e0cd6bfb85e8d80755987e17608000000000017a914ef20c4471b54fc47c93d587a318d351e93fbc13b8700000000",
      "txid": "620c724890f76b802714d786d5d3fe13a89106d81e93b74c4eafd6dc04179f37",
      "hash": "620c724890f76b802714d786d5d3fe13a89106d81e93b74c4eafd6dc04179f37",
      "depends": [
      ],
      "fee": 3493,
      "sigops": 8,
      "weight": 1328
    }
  ],
  "coinbaseaux": {
    "flags": ""
  },
  "coinbasevalue": 2500009943,
  "longpollid": "b83b698bed0897ac94819041aec857d1a26a567bf7bf046d60849d5ccf24155e19620",
  "target": "00000000a9490000000000000000000000000000000000000000000000000000",
  "mintime": 1581747579,
  "mutable": [
    "time",
    "transactions",
    "prevblock"
  ],
  "noncerange": "00000000ffffffff",
  "sigoplimit": 80000,
  "sizelimit": 4000000,
  "weightlimit": 4000000,
  "curtime": 1581749398,
  "bits": "1d00a949",
  "height": 1369986
}

`

	var rpcData daemonManager.GetBlockTemplate
	json.Unmarshal([]byte(data), &rpcData)

	pk := utils.AddressToScript("QPxrDq3sorCk8DWaYX2GeCkxoePhm1asyY")
	placeholder, _ := hex.DecodeString("f000000ff111111f")

	log.Println(hex.EncodeToString(utils.PackUint32LE(uint32(0))))

	gens := CreateGeneration(&rpcData, pk, placeholder, "POW", true, make(map[string]float64))

	log.Println("0: ", hex.EncodeToString(gens[0]))
	log.Println("1: ", hex.EncodeToString(gens[1]))

	t1, _ := hex.DecodeString("02000000010000000000000000000000000000000000000000000000000000000000000000ffffffff1f0382e71404")
	if !bytes.Contains(gens[0], t1) {
		t.Fail()
	}

	if hex.EncodeToString(gens[1]) != "0c2f627920436f6d6d616e642f0000000001d71f0395000000001976a91424da8749fde8fcdcde60ba1c5afea8d2bd4a4f2688ac000000000a627920436f6d6d616e64" {
		t.Fail()
	}

}

func TestGenerateOutputTransactions(t *testing.T) {
	publicKey := utils.AddressToScript("QPxrDq3sorCk8DWaYX2GeCkxoePhm1asyY")
	recipients := make(map[string]float64)
	data := `
{
  "capabilities": [
    "proposal"
  ],
  "version": 536870912,
  "rules": [
    "csv",
    "segwit"
  ],
  "vbavailable": {
  },
  "vbrequired": 0,
  "previousblockhash": "b83b698bed0897ac94819041aec857d1a26a567bf7bf046d60849d5ccf24155e",
  "transactions": [
    {
      "data": "01000000012f8975c900f56662f35c317a0669fecc5fe0e1fb8ee53f4de72f1cb68c07e606010000008a473044022061a9ac17f269f3c69e18b5d67dfa6bf8b6a5a60eb7f9b0c992ffaeb66b5b88fb02202bb6fd7eb539302d97f4b8604bc822c91747e1cb365fecc91b37526b6b8c2c25014104fe67366f857106ee7b4cc48abb4dabd46302e12fe4140f4c933b92bd3ce75b1f4ae45055312f9a6c5ddc1f8d94d4f6d11e2a13372bcd6bfd651e48997b0f767effffffff02e8030000000000001976a914dffec839eba107e556d6c4f25f90765b3d10583288acbb60da04000000001976a914bdd83cf3ab8b7a57ff9b841752c1ae764f2a02ee88ac00000000",
      "txid": "f9b8b0bdd0dc38b2a707faf89acf064f543c3a88d39f54fb126cbd084ffb5ed9",
      "hash": "f9b8b0bdd0dc38b2a707faf89acf064f543c3a88d39f54fb126cbd084ffb5ed9",
      "depends": [
      ],
      "fee": 6450,
      "sigops": 8,
      "weight": 1028
    },
    {
      "data": "0200000001979a795a82096fc375487778939d9193bb284c58525e5df9c3a404c81c9220ef01000000d9004730440220086f0b09ded442c84e602520f5a8b38b41a1bc860fb595bd47834c20fa8db39402200a40cb86c15198302cabfd5c620c24fa6ac9ac5d946394e37dd3f9960b65a0e701473044022049bc0be153a4535196f73455bf82667956f2089019db4eeb57cb35649d8f69b202206ddd411917cb3e54f7b9a89c0693c971a6499eb6e421dce1d5fa9358300525d301475221025ad7eedea4c87b98463b8c7316c139f94c0e75fe4c849f42dab112479e1a1bb7210257591ace4d6a9fc94b8114cffd84df9bd0349c974a792580f7f5afb74f5ba94952ae0000000002102700000000000017a914b75a640760f2caae367c0e0cd6bfb85e8d80755987e17608000000000017a914ef20c4471b54fc47c93d587a318d351e93fbc13b8700000000",
      "txid": "620c724890f76b802714d786d5d3fe13a89106d81e93b74c4eafd6dc04179f37",
      "hash": "620c724890f76b802714d786d5d3fe13a89106d81e93b74c4eafd6dc04179f37",
      "depends": [
      ],
      "fee": 3493,
      "sigops": 8,
      "weight": 1328
    }
  ],
  "coinbaseaux": {
    "flags": ""
  },
  "coinbasevalue": 2500009943,
  "longpollid": "b83b698bed0897ac94819041aec857d1a26a567bf7bf046d60849d5ccf24155e19620",
  "target": "00000000a9490000000000000000000000000000000000000000000000000000",
  "mintime": 1581747579,
  "mutable": [
    "time",
    "transactions",
    "prevblock"
  ],
  "noncerange": "00000000ffffffff",
  "sigoplimit": 80000,
  "sizelimit": 4000000,
  "weightlimit": 4000000,
  "curtime": 1581749398,
  "bits": "1d00a949",
  "height": 1369986
}

`
	var rpcData daemonManager.GetBlockTemplate
	json.Unmarshal([]byte(data), &rpcData)
	log.Println(hex.EncodeToString(GenerateOutputTransactions(publicKey, recipients, &rpcData)))
}
