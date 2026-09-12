package quantus

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

const alpn = "quantus-miner/2"
const maxMessageSize = 1024

type request struct {
	JobID      string `json:"job_id"`
	MiningHash string `json:"mining_hash"`
	Difficulty string `json:"difficulty"`
}
type result struct {
	Status      string  `json:"status"`
	JobID       string  `json:"job_id"`
	Nonce       *string `json:"nonce"`
	Work        *string `json:"work"`
	HashCount   uint64  `json:"hash_count"`
	ElapsedTime float64 `json:"elapsed_time"`
}
type message struct {
	Ready *struct {
		Token string `json:"token"`
	} `json:"Ready,omitempty"`
	NewJob    *request `json:"NewJob,omitempty"`
	JobResult *result  `json:"JobResult,omitempty"`
}

func readMessage(r io.Reader) (message, error) {
	var m message
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return m, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > maxMessageSize {
		return m, errors.New("quantus: invalid frame size")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return m, err
	}
	err := json.Unmarshal(b, &m)
	count := 0
	if m.Ready != nil {
		count++
	}
	if m.NewJob != nil {
		count++
	}
	if m.JobResult != nil {
		count++
	}
	if err == nil && count != 1 {
		err = errors.New("quantus: invalid message")
	}
	return m, err
}
func writeMessage(w io.Writer, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > maxMessageSize {
		return errors.New("quantus: frame too large")
	}
	frame := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(frame, uint32(len(b)))
	copy(frame[4:], b)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}
func pinnedTLS(fingerprint string) (*tls.Config, error) {
	pin, err := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(fingerprint), ":", ""))
	if err != nil || len(pin) != 32 {
		return nil, errors.New("quantus: invalid TLS SHA256 fingerprint")
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: "localhost", NextProtos: []string{alpn},
		// The node uses a self-signed certificate. Authenticate its exact DER hash.
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("quantus: missing node certificate")
			}
			hash := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(hash[:], pin) != 1 {
				return errors.New("quantus: node certificate fingerprint mismatch")
			}
			return nil
		},
	}, nil
}
