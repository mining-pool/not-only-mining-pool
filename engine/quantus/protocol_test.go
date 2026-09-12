package quantus

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	// Rust's externally-tagged enum representation, not JSON-RPC.
	raw := `{"NewJob":{"job_id":"1","mining_hash":"` + strings.Repeat("00", 32) + `","difficulty":"1"}}`
	binary.Write(&buf, binary.BigEndian, uint32(len(raw)))
	buf.WriteString(raw)
	m, err := readMessage(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.NewJob == nil || m.NewJob.JobID != "1" {
		t.Fatal(m)
	}
	if err = writeMessage(&buf, m); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes()[4:], []byte(raw)) {
		t.Fatalf("wire shape %s", buf.Bytes()[4:])
	}
	for _, n := range []uint32{0, 1025, 0xffffffff} {
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, n)
		if _, err = readMessage(&buf); err == nil {
			t.Fatal("accepted bad length", n)
		}
	}
	buf.Reset()
	binary.Write(&buf, binary.BigEndian, uint32(10))
	buf.WriteString("{")
	if _, err = readMessage(&buf); err == nil {
		t.Fatal("accepted truncated payload")
	}
}
func TestParseJob(t *testing.T) {
	for _, d := range []string{"0", "-1", "+1", "1.0", "NaN", "", strings.Repeat("9", 155)} {
		if _, err := parseJob(&request{JobID: "a", MiningHash: strings.Repeat("00", 32), Difficulty: d}); err == nil {
			t.Fatal("accepted difficulty", d)
		}
	}
	if _, err := parseJob(&request{JobID: "a", MiningHash: "00", Difficulty: "1"}); err == nil {
		t.Fatal("accepted short header")
	}
}
