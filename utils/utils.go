package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/c0mm4nd/go-bech32"
	logging "github.com/ipfs/go-log/v2"
	"github.com/mr-tron/base58"
)

var log = logging.Logger("utils")

func RandPositiveInt64() int64 {
	randomNumBytes := make([]byte, 8)
	_, err := rand.Read(randomNumBytes)
	if err != nil {
		log.Error(err)
	}

	i := int64(binary.LittleEndian.Uint64(randomNumBytes))
	if i > 0 {
		return i
	} else {
		return -i
	}
}

func RandHexUint64() string {
	randomNumBytes := make([]byte, 8)
	_, err := rand.Read(randomNumBytes)
	if err != nil {
		log.Error(err)
	}

	return hex.EncodeToString(randomNumBytes)
}

func PackUint64LE(n uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return b
}

func PackInt64BE(n int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
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

func PackInt32BE(n int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n))
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
	}

	if n <= 0xFFFF {
		buff := make([]byte, 3)
		buff[0] = 0xFD
		binary.LittleEndian.PutUint16(buff[1:], uint16(n))
		return buff
	}

	if n <= 0xFFFFFFFF {
		buff := make([]byte, 5)
		buff[0] = 0xFE
		binary.LittleEndian.PutUint32(buff[1:], uint32(n))
		return buff
	}

	buff := make([]byte, 9)
	buff[0] = 0xFF
	binary.LittleEndian.PutUint64(buff[1:], uint64(n))
	return buff
}

func VarStringBytes(str string) []byte {
	bStr := []byte(str)
	return bytes.Join([][]byte{
		VarIntBytes(uint64(len(bStr))),
		bStr,
	}, nil)
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
		log.Error(err)
	}

	copy(container, fromHex)

	return ReverseBytes(container)
}

func ReverseBytes(b []byte) []byte {
	_b := make([]byte, len(b))
	copy(_b, b)

	for i, j := 0, len(_b)-1; i < j; i, j = i+1, j-1 {
		_b[i], _b[j] = _b[j], _b[i]
	}
	return _b
}

// range steps between [start, end)
func Range(start, stop, step int) []int {
	if (step > 0 && start >= stop) || (step < 0 && start <= stop) {
		return []int{}
	}

	result := make([]int, 0)
	i := start
	for {
		if step > 0 {
			if i < stop {
				result = append(result, i)
			} else {
				break
			}
		} else {
			if i > stop {
				result = append(result, i)
			} else {
				break
			}
		}
		i += step
	}

	return result
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
		if bytes.Equal(element, v) {
			return k
		}
	}
	return -1 // not found.
}

func StringsIndexOf(data []string, element string) int {
	for k, v := range data {
		if strings.Compare(element, v) == 0 {
			return k
		}
	}
	return -1 // not found.
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
	_b := make([]byte, len(b))
	copy(_b, b)

	for i := 0; i < 8; i++ {
		binary.LittleEndian.PutUint32(_b[i*4:], binary.BigEndian.Uint32(_b[i*4:]))
	}
	return ReverseBytes(_b)
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

// For POW coins - used to format wallet address for use in generation transaction's output
// Works for p2pkh only
func P2PKHAddressToScript(addr string) []byte {
	decoded, err := base58.FastBase58Decoding(addr)
	if decoded == nil || err != nil {
		log.Fatal("base58 decode failed for " + addr)
	}

	if len(decoded) != 25 {
		log.Panic("invalid address length for " + addr)
	}

	publicKey := decoded[1 : len(decoded)-4]

	return bytes.Join([][]byte{
		{0x76, 0xA9, 0x14},
		publicKey,
		{0x88, 0xAC},
	}, nil)
}

func P2SHAddressToScript(addr string) []byte {
	decoded, err := base58.FastBase58Decoding(addr)
	if decoded == nil || err != nil {
		log.Fatal("base58 decode failed for " + addr)
	}

	if len(decoded) != 25 {
		log.Panic("invalid address length for " + addr)
	}

	publicKey := decoded[1 : len(decoded)-4]

	return bytes.Join([][]byte{
		{0xA9, 0x14},
		publicKey,
		{0x87},
	}, nil)
}

func P2WSHAddressToScript(addr string) []byte {
	_, decoded, err := bech32.Decode(addr)
	if decoded == nil || err != nil {
		log.Fatal("bech32 decode failed for " + addr)
	}
	witnessProgram, err := bech32.ConvertBits(decoded[1:], 5, 8, true)
	if err != nil {
		log.Panic("")
	}

	return bytes.Join([][]byte{
		{0x00, 0x14},
		witnessProgram,
	}, nil)
}

func ScriptPubKeyToScript(addr string) []byte {
	decoded, err := hex.DecodeString(addr)
	if decoded == nil || err != nil {
		log.Fatal("hex decode failed for " + addr)
	}
	return decoded
}

func HexDecode(b []byte) []byte {
	dst := make([]byte, hex.DecodedLen(len(b)))
	_, err := hex.Decode(dst, b)
	if err != nil {
		log.Panic("failed to decode hex: ", string(b))
	}

	return dst
}

func HexEncode(b []byte) []byte {
	dst := make([]byte, hex.EncodedLen(len(b)))
	hex.Encode(dst, b)

	return dst
}

func Jsonify(i interface{}) []byte {
	r, err := json.Marshal(i)
	if err != nil {
		log.Error("Jsonify: ", err)
		return nil
	}

	return r
}

func JsonifyIndentString(i interface{}) string {
	r, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		log.Error("JsonifyIndentString: ", err)
		return ""
	}
	return string(r)
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
	byteUnits := []string{" H", " KH", " MH", " GH", " TH", " PH", " EH", " ZH", " YH"}
	for hashrate > 1000 {
		i++
		hashrate = hashrate / 1000
		if i+1 == len(byteUnits) {
			break
		}
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
		log.Error(err)
	}

	return str
}

func FixedLenStringBytes(s string, l int) []byte {
	b := make([]byte, l)
	copy(b, s)
	return b
}

func CommandStringBytes(s string) []byte {
	return FixedLenStringBytes(s, 12)
}

func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}
