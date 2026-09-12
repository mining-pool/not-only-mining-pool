package quantus

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"
)

func payoutAddress(worker string) (string, error) {
	a := strings.SplitN(worker, ".", 2)[0]
	b, err := base58.Decode(a)
	// SS58 network 189 uses the two-byte prefix 0x6f,0x40.
	if err != nil || len(b) != 36 || b[0] != 0x6f || b[1] != 0x40 {
		return "", errors.New("expected a Quantus SS58 address (prefix 189)")
	}
	h := blake2b.Sum512(append([]byte("SS58PRE"), b[:34]...))
	if !bytes.Equal(h[:2], b[34:]) {
		return "", errors.New("invalid SS58 checksum")
	}
	return a, nil
}

type chainHeader struct {
	ParentHash     string `json:"parentHash"`
	Number         string `json:"number"`
	StateRoot      string `json:"stateRoot"`
	ExtrinsicsRoot string `json:"extrinsicsRoot"`
	ZKTreeRoot     string `json:"zkTreeRoot"`
	Digest         struct {
		Logs []string `json:"logs"`
	} `json:"digest"`
}

func hexBytes(s string, size int) ([]byte, error) {
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(b) != size {
		return nil, errors.New("invalid chain hex field")
	}
	return b, nil
}

// miningIdentity reproduces qp_header::Header::hash after removing the PoW seal.
// Quantus headers include zkTreeRoot and do not use Substrate's Blake2 header hash.
func miningIdentity(h chainHeader) (string, string, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(h.Number, "0x"), 16, 32)
	if err != nil {
		return "", "", err
	}
	var felts []uint64
	for i, field := range []string{h.ParentHash, h.StateRoot, h.ExtrinsicsRoot, h.ZKTreeRoot} {
		b, err := hexBytes(field, 32)
		if err != nil {
			return "", "", err
		}
		for j := 0; j < 32; j += 8 {
			felts = append(felts, binary.LittleEndian.Uint64(b[j:])%goldilocks)
		}
		if i == 0 {
			felts = append(felts, n)
		}
	}
	if len(h.Digest.Logs) == 0 || len(h.Digest.Logs) > 63 {
		return "", "", errors.New("invalid digest logs")
	}
	var logs []byte
	var nonce string
	count := 0
	for i, log := range h.Digest.Logs {
		b, err := hex.DecodeString(strings.TrimPrefix(log, "0x"))
		if err != nil {
			return "", "", err
		}
		// SCALE DigestItem::Seal([p,o,w,_], Vec<u8>(64)).
		if len(b) == 71 && bytes.Equal(b[:7], []byte{5, 'p', 'o', 'w', '_', 1, 1}) {
			if nonce != "" || i != len(h.Digest.Logs)-1 {
				return "", "", errors.New("noncanonical PoW seal")
			}
			nonce = hex.EncodeToString(b[7:])
			continue
		}
		logs = append(logs, b...)
		count++
	}
	if nonce == "" {
		return "", "", errors.New("missing PoW seal")
	}
	if len(logs)+1 > 110 {
		return "", "", errors.New("digest commitment window exceeded")
	}
	var padded [112]byte
	padded[0] = byte(count << 2)
	copy(padded[1:], logs)
	padded[110] = 1 // bytes_to_felts serialization terminator
	for j := 0; j < 112; j += 4 {
		felts = append(felts, uint64(binary.LittleEndian.Uint32(padded[j:])))
	}
	felts = append(felts, 1) // sponge padding
	var state [12]uint64
	for i, v := range felts {
		state[i%8] = add(state[i%8], v)
		if i%8 == 7 {
			permute(&state)
		}
	}
	if len(felts)%8 != 0 {
		permute(&state)
	}
	var out [32]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], state[i])
	}
	return hex.EncodeToString(out[:]), nonce, nil
}
