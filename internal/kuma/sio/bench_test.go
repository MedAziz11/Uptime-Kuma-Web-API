package sio

import (
	"encoding/json"
	"testing"
)

var benchAck = []byte(`30[{"ok":true,"monitors":[{"id":1,"name":"a","url":"https://example.com","interval":60,"active":true,"tags":[]},{"id":2,"name":"b","url":"https://example.org","interval":60,"active":false,"tags":[{"id":1,"name":"x"}]}]}]`)

func BenchmarkParsePacket(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAck)))
	for b.Loop() {
		if _, err := ParsePacket(benchAck); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParseAndSplit(b *testing.B) {
	var args []json.RawMessage
	b.ReportAllocs()
	b.SetBytes(int64(len(benchAck)))
	for b.Loop() {
		p, err := ParsePacket(benchAck)
		if err != nil {
			b.Fatal(err)
		}
		if args, err = SplitArray(args[:0], p.Data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEncodeEvent(b *testing.B) {
	c := &Conn{}
	c.frames.New = newFrame
	arg := map[string]any{"id": 1, "name": "monitor", "url": "https://example.com", "interval": 60}
	raw := json.RawMessage(`{"id":1,"name":"monitor","url":"https://example.com","interval":60}`)
	for name, a := range map[string]any{"map": arg, "raw": raw, "int": 42} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := uint64(0); b.Loop(); i++ {
				f := c.frames.Get().(*frame)
				if err := f.encodeEvent(i, "editMonitor", []any{a}); err != nil {
					b.Fatal(err)
				}
				c.putFrame(f)
			}
		})
	}
}

func BenchmarkAppendPacket(b *testing.B) {
	p := Packet{Type: PacketAck, ID: 12345, HasID: true, Data: []byte(`[{"ok":true}]`)}
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	for b.Loop() {
		buf = AppendPacket(buf[:0], p)
	}
}
