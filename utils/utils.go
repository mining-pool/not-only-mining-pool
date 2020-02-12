package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/decred/base58"
	"log"
	"math/big"
	"strconv"
	"strings"
)

func RandPositiveInt64() int64 {
	randomNumBytes := make([]byte, 8)
	_, err := rand.Read(randomNumBytes)
	if err != nil {
		log.Println(err)
	}

	i := int64(binary.LittleEndian.Uint64(randomNumBytes))
	if i > 0 {
		return i
	} else {
		return -i
	}
}

func PackUint64LE(n uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return b
}

func PackUint64BE(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

func PackUint32LE(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return b
}

func PackUint32BE(n uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return b
}

func PackUint16LE(n uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, n)
	return b
}

func PackUint16BE(n uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, n)
	return b
}

func VarIntBytes(n uint64) []byte {
	if n < 0xFD {
		return []byte{byte(n)}
	} else if n < 0xFFFF {
		buff := make([]byte, 3)
		buff[0] = 0xfd
		binary.LittleEndian.PutUint16(buff[1:], uint16(n))
		return buff
	} else if n < 0xFFFFFFFF {
		buff := make([]byte, 5)
		buff[0] = 0xFE
		binary.LittleEndian.PutUint32(buff[1:], uint32(n))
		return buff
	} else {
		buff := make([]byte, 9)
		buff[0] = 0xFF
		binary.LittleEndian.PutUint64(buff[1:], uint64(n))
		return buff
	}
}

func SerializeString(s string) []byte {
	if len(s) < 253 {
		return bytes.Join([][]byte{
			{byte(len(s))},
			[]byte(s),
		}, nil)
	} else if len(s) < 0x10000 {
		return bytes.Join([][]byte{
			{253},
			PackUint16LE(uint16(len(s))),
			[]byte(s),
		}, nil)
	} else if len(s) < 0x100000000 {
		return bytes.Join([][]byte{
			{254},
			PackUint32LE(uint32(len(s))),
			[]byte(s),
		}, nil)
	} else {
		return bytes.Join([][]byte{
			{255},
			PackUint64LE(uint64(len(s))),
			[]byte(s),
		}, nil)
	}
}

func SerializeNumber(n uint64) []byte {
	if n >= 1 && n <= 16 {
		return []byte{
			0x50 + byte(n),
		}
	}

	l := 1
	buff := make([]byte, 9)
	for n > 0x7f {
		buff[l] = byte(n & 0xff)
		l++
		n >>= 8
	}
	buff[0] = byte(l)
	buff[l] = byte(n)

	return buff[0 : l+1]
}

func Uint256BytesFromHash(h string) []byte {
	container := make([]byte, 32)
	fromHex, err := hex.DecodeString(h)
	if err != nil {
		log.Println(err)
	}

	copy(container, fromHex)

	return ReverseBytes(container)
}

func ReverseBytes(b []byte) []byte {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return b
}

func Range(start, end, step int) []int {
	if step <= 0 || end < start {
		return []int{}
	}
	s := make([]int, 0, 1+(end-start)/step)
	for start <= end {
		s = append(s, start)
		start += step
	}
	return s
}

func Sha256(b []byte) []byte {
	b32 := sha256.Sum256(b)
	return b32[:]
}

func Sha256d(b []byte) []byte {
	return Sha256(Sha256(b))
}

func BytesIndexOf(data [][]byte, element []byte) int {
	for k, v := range data {
		if bytes.Compare(element, v) == 0 {
			return k
		}
	}
	return -1 //not found.
}

func StringsIndexOf(data []string, element string) int {
	for k, v := range data {
		if strings.Compare(element, v) == 0 {
			return k
		}
	}
	return -1 //not found.
}

func BigIntFromBitsHex(bits string) *big.Int {
	bBits, err := hex.DecodeString(bits)
	if err != nil {
		log.Panic(err)
	}
	return BigIntFromBitsBytes(bBits)
}

func BigIntFromBitsBytes(bits []byte) *big.Int {
	bytesNumber := bits[0]

	bigBits := new(big.Int).SetBytes(bits[1:])
	return new(big.Int).Mul(bigBits, new(big.Int).Exp(big.NewInt(2), big.NewInt(8*int64(bytesNumber-3)), nil))
}

// LE <-> BE
func ReverseByteOrder(b []byte) []byte {
	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(b[i*4:], binary.BigEndian.Uint32(b[i*4:]))
	}
	return ReverseBytes(b)
}

// For POS coins - used to format wallet address for use in generation transaction's output
func PublicKeyToScript(key string) []byte {
	if len(key) != 66 {
		log.Panic("Invalid public key: " + key)
	}

	bKey := make([]byte, 35)
	bKey[0] = 0x21
	bKey[34] = 0xAC
	b, _ := hex.DecodeString(key)
	copy(bKey[1:], b)

	return bKey

}

func AddressToScript(addr string) []byte {
	decoded := base58.Decode(addr)

	if len(decoded) < 25 {
		log.Panic("invalid address length for " + addr)
	}

	if decoded == nil {
		log.Panic("base58 decode failed for " + addr)
	}

	publicKey := decoded[1 : len(decoded)-4]

	return bytes.Join([][]byte{
		{0x76, 0xA9, 0x14},
		publicKey,
		{0x88, 0xAC},
	}, nil)
}

func HexDecode(b []byte) []byte {
	dst := make([]byte, hex.DecodedLen(len(b)))
	_, err := hex.Decode(dst, b)
	if err != nil {
		log.Panic("failed to decode hex:", string(b))
	}

	return dst
}

func HexEncode(b []byte) []byte {
	dst := make([]byte, hex.EncodedLen(len(b)))
	hex.Encode(dst, b)

	return dst
}

var nullBytes, _ = json.Marshal(json.RawMessage("null"))

func Jsonify(i interface{}) []byte {
	r, err := json.Marshal(i)
	if err != nil {
		log.Println("Jsonify:", err)
		return nil
	}

	return r
}

func JsonifyIndentString(i interface{}) string {
	r, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		log.Println("JsonifyIndentString:", err)
		return ""
	}
	return string(r)
}

func IsNull(b json.RawMessage) bool {
	var v interface{}
	_ = json.Unmarshal(b, &v)
	if v == nil {
		return true
	}

	return false
}

func IsNotNull(b json.RawMessage) bool {
	var v interface{}
	_ = json.Unmarshal(b, &v)
	if v == nil {
		return false
	}

	return true
}

func SatoshisToCoins(satoshis uint64, magnitude int, coinPrecision int) float64 {
	coins := float64(satoshis) / float64(magnitude)
	coins, _ = strconv.ParseFloat(fmt.Sprintf("%."+strconv.Itoa(coinPrecision)+"f", coins), 64)
	return coins
}

func CoinsToSatoshis(coins float64, magnitude int, coinPrecision int) uint64 {
	return uint64(coins * float64(magnitude))
}

func GetReadableHashRateString(hashrate float64) string {
	i := 0
	byteUnits := []string{" H", " KH", " MH", " GH", " TH", " PH"}
	for hashrate > 1024 {
		hashrate = hashrate / 1024
		i++
	}

	return strconv.FormatFloat(hashrate, 'f', 7, 64) + byteUnits[i]
}

func MiningKeyToScript(addrMiningKey string) []byte {
	b, err := hex.DecodeString(addrMiningKey)
	if err != nil {
		log.Fatal("Failed to decode addr mining key: ", addrMiningKey)
	}

	return bytes.Join([][]byte{
		{0x76, 0xA9, 0x14},
		b,
		{0x88, 0xAC},
	}, nil)
}

func RawJsonToString(raw json.RawMessage) string {
	var str string
	err := json.Unmarshal(raw, &str)
	if err != nil {
		log.Println(err)
	}

	return str

}
