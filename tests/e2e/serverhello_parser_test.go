package e2e

import (
	"bytes"
	"strings"
	"testing"
)

// serverHelloMessage builds a ServerHello handshake message (header and body).
func serverHelloMessage(random byte, sessionID []byte) []byte {
	body := []byte{3, 3}
	body = append(body, bytes.Repeat([]byte{random}, 32)...)
	body = append(body, byte(len(sessionID)))
	body = append(body, sessionID...)
	body = append(body, 0xc0, 0x2f, 0) // suite, null compression
	return append([]byte{2, byte(len(body) >> 16), byte(len(body) >> 8), byte(len(body))}, body...)
}

func tlsRecord(typ byte, payload []byte) []byte {
	return append([]byte{typ, 3, 3, byte(len(payload) >> 8), byte(len(payload))}, payload...)
}

func TestServerHelloParser(t *testing.T) {
	id := bytes.Repeat([]byte{0xab}, 32)
	msg := serverHelloMessage(7, id)
	empty := serverHelloMessage(9, nil)
	oversized := []byte{2, 0xff, 0xff, 0xff, 3, 3}

	cases := []struct {
		name    string
		input   []byte
		random  byte
		id      []byte
		wantErr string
	}{
		{"non-empty session id", tlsRecord(22, msg), 7, id, ""},
		{"empty session id", tlsRecord(22, empty), 9, nil, ""},
		{"header split across records", append(tlsRecord(22, msg[:2]), tlsRecord(22, msg[2:])...), 7, id, ""},
		{"header split 1+1+2 bytes", bytes.Join([][]byte{tlsRecord(22, msg[:1]), tlsRecord(22, msg[1:2]), tlsRecord(22, msg[2:])}, nil), 7, id, ""},
		{"body split across records", append(tlsRecord(22, msg[:20]), tlsRecord(22, msg[20:])...), 7, id, ""},
		{"truncated in body", tlsRecord(22, msg)[:30], 0, nil, "EOF"},
		{"truncated record header", []byte{22, 3}, 0, nil, "EOF"},
		{"truncated after first record", tlsRecord(22, msg[:20]), 0, nil, "EOF"},
		{"empty input", nil, 0, nil, "EOF"},
		{"oversized message length", tlsRecord(22, oversized), 0, nil, "out of range"},
		{"oversized record length", []byte{22, 3, 3, 0xff, 0xff}, 0, nil, "exceeds"},
		{"wrong record type", tlsRecord(23, msg), 0, nil, "record type 23"},
		{"wrong type mid-message", append(tlsRecord(22, msg[:20]), tlsRecord(23, msg[20:])...), 0, nil, "record type 23"},
		{"alert", tlsRecord(21, []byte{2, 40}), 0, nil, "level 2 description 40"},
		{"not a ServerHello", tlsRecord(22, []byte{11, 0, 0, 0}), 0, nil, "not ServerHello"},
		{"session id longer than body", tlsRecord(22, append([]byte{2, 0, 0, 38, 3, 3}, append(bytes.Repeat([]byte{1}, 32), 40, 1, 2, 3)...)), 0, nil, "session id length"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseServerHello(bytes.NewReader(c.input))
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.random != string(bytes.Repeat([]byte{c.random}, 32)) || got.sessionID != string(c.id) {
				t.Fatalf("got random %x id %x", got.random, got.sessionID)
			}
		})
	}
}

func TestServerHelloParserTooManyRecords(t *testing.T) {
	var in []byte
	for i := 0; i < maxHelloRecords+4; i++ {
		in = append(in, tlsRecord(22, nil)...) // empty handshake records
	}
	if _, err := parseServerHello(bytes.NewReader(in)); err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("error = %v", err)
	}
}

func TestDistinctHelloProblems(t *testing.T) {
	h := func(r, id string) robustServerHello { return robustServerHello{random: r, sessionID: id} }
	if p := distinctHelloProblems([]robustServerHello{h("a", ""), h("b", ""), h("c", ""), h("d", "x")}); len(p) != 0 {
		t.Errorf("several empty session ids reported: %v", p)
	}
	if p := distinctHelloProblems([]robustServerHello{h("a", "x"), h("b", "x")}); len(p) != 1 {
		t.Errorf("duplicate non-empty id: %v", p)
	}
	if p := distinctHelloProblems([]robustServerHello{h("a", ""), h("a", "")}); len(p) != 1 {
		t.Errorf("duplicate random with empty ids: %v", p)
	}
}
