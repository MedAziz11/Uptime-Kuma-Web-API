package sio

import (
	"bytes"
	"testing"
)

// FuzzParsePacket checks that the parser never panics, that what it accepts
// re-encodes to a packet it parses identically, and that accepted EVENT and
// ACK payloads can be split into arguments.
func FuzzParsePacket(f *testing.F) {
	for _, tc := range parseCases {
		f.Add([]byte(tc.in))
	}
	for _, s := range []string{`2["a\"",{"b":[1,{"c":"]"}]}]`, `51-/x,7["a"]`, `0/,`, `318446744073709551616[]`, "2[\"\\u00e9\"]"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := ParsePacket(b)
		if err != nil {
			return
		}
		enc := AppendPacket(nil, p)
		q, err := ParsePacket(enc)
		if err != nil {
			t.Fatalf("re-encoded %q as %q, which does not parse: %v", b, enc, err)
		}
		if p.Type != q.Type || p.Namespace != q.Namespace || p.Attachments != q.Attachments ||
			p.ID != q.ID || p.HasID != q.HasID || !bytes.Equal(p.Data, q.Data) {
			t.Fatalf("round trip of %q: %+v != %+v", b, p, q)
		}
		switch p.Type {
		case PacketEvent, PacketBinaryEvent:
			args, err := SplitArray(nil, p.Data)
			if err != nil || len(args) == 0 {
				t.Fatalf("event %q: SplitArray = %q, %v", b, args, err)
			}
			if _, err := EventName(args[0]); err != nil {
				t.Fatalf("event %q: EventName: %v", b, err)
			}
		case PacketAck, PacketBinaryAck:
			if _, err := SplitArray(nil, p.Data); err != nil {
				t.Fatalf("ack %q: SplitArray: %v", b, err)
			}
		}
	})
}
