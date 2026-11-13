package sio

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Frames from the Socket.IO protocol 5 specification and from Uptime Kuma
// 2.5.11's handlers (login acks, multi-argument pushes).
var parseCases = []struct {
	name string
	in   string
	want Packet
}{
	{"connect", `0`, Packet{Type: PacketConnect}},
	{"connect answer", `0{"sid":"oSO0OpakMV_3jnilAAAA"}`, Packet{Type: PacketConnect, Data: []byte(`{"sid":"oSO0OpakMV_3jnilAAAA"}`)}},
	{"connect admin", `0/admin,{"sid":"oSO0OpakMV_3jnilAAAA"}`, Packet{Type: PacketConnect, Namespace: "/admin", Data: []byte(`{"sid":"oSO0OpakMV_3jnilAAAA"}`)}},
	{"connect explicit default namespace", `0/,`, Packet{Type: PacketConnect}},
	{"connect error object", `4{"message":"Not authorized"}`, Packet{Type: PacketConnectError, Data: []byte(`{"message":"Not authorized"}`)}},
	{"connect error string", `4"Not authorized"`, Packet{Type: PacketConnectError, Data: []byte(`"Not authorized"`)}},
	{"disconnect", `1`, Packet{Type: PacketDisconnect}},
	{"disconnect admin", `1/admin,`, Packet{Type: PacketDisconnect, Namespace: "/admin"}},
	{"disconnect admin without comma", `1/admin`, Packet{Type: PacketDisconnect, Namespace: "/admin"}},
	{"event", `2["foo"]`, Packet{Type: PacketEvent, Data: []byte(`["foo"]`)}},
	{"event admin", `2/admin,["bar"]`, Packet{Type: PacketEvent, Namespace: "/admin", Data: []byte(`["bar"]`)}},
	{"event with ack id", `212["foo"]`, Packet{Type: PacketEvent, ID: 12, HasID: true, Data: []byte(`["foo"]`)}},
	{"event with spaces", "2 [ \"foo\" , 1 ]", Packet{Type: PacketEvent, Data: []byte(" [ \"foo\" , 1 ]")}},
	{"kuma uptime push", `2["uptime",1,24,0.9987]`, Packet{Type: PacketEvent, Data: []byte(`["uptime",1,24,0.9987]`)}},
	{"ack", `313["bar"]`, Packet{Type: PacketAck, ID: 13, HasID: true, Data: []byte(`["bar"]`)}},
	{"ack admin", `3/admin,13["bar"]`, Packet{Type: PacketAck, Namespace: "/admin", ID: 13, HasID: true, Data: []byte(`["bar"]`)}},
	{"ack empty", `37[]`, Packet{Type: PacketAck, ID: 7, HasID: true, Data: []byte(`[]`)}},
	{"kuma login ack", `30[{"ok":true,"token":"eyJ.x.y"}]`, Packet{Type: PacketAck, ID: 0, HasID: true, Data: []byte(`[{"ok":true,"token":"eyJ.x.y"}]`)}},
	{"kuma 2fa ack", `30[{"tokenRequired":true}]`, Packet{Type: PacketAck, HasID: true, Data: []byte(`[{"tokenRequired":true}]`)}},
	{"max ack id", `318446744073709551615[]`, Packet{Type: PacketAck, ID: 1<<64 - 1, HasID: true, Data: []byte(`[]`)}},
	{"binary event", `51-["baz",{"_placeholder":true,"num":0}]`, Packet{Type: PacketBinaryEvent, Attachments: 1, Data: []byte(`["baz",{"_placeholder":true,"num":0}]`)}},
	{"binary event admin", `52-/admin,["baz",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`, Packet{Type: PacketBinaryEvent, Attachments: 2, Namespace: "/admin", Data: []byte(`["baz",{"_placeholder":true,"num":0},{"_placeholder":true,"num":1}]`)}},
	{"binary ack", `61-15["bar",{"_placeholder":true,"num":0}]`, Packet{Type: PacketBinaryAck, Attachments: 1, ID: 15, HasID: true, Data: []byte(`["bar",{"_placeholder":true,"num":0}]`)}},
}

func TestParsePacket(t *testing.T) {
	for _, tc := range parseCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePacket([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParsePacket(%q): %v", tc.in, err)
			}
			if got.Type != tc.want.Type || got.Namespace != tc.want.Namespace || got.Attachments != tc.want.Attachments ||
				got.ID != tc.want.ID || got.HasID != tc.want.HasID || string(got.Data) != string(tc.want.Data) {
				t.Fatalf("ParsePacket(%q) = %+v (data %q), want %+v (data %q)", tc.in, got, got.Data, tc.want, tc.want.Data)
			}
		})
	}
}

func TestAppendPacketRoundTrip(t *testing.T) {
	for _, tc := range parseCases {
		if strings.Contains(tc.name, "spaces") || strings.Contains(tc.name, "explicit default") || strings.Contains(tc.name, "without comma") {
			continue // not canonical encodings
		}
		t.Run(tc.name, func(t *testing.T) {
			if got := string(AppendPacket(nil, tc.want)); got != tc.in {
				t.Fatalf("AppendPacket = %q, want %q", got, tc.in)
			}
		})
	}
	if got := string(AppendPacket([]byte("4"), Packet{Type: PacketEvent, Namespace: "/", Data: []byte(`["x"]`)})); got != `42["x"]` {
		t.Fatalf("default namespace written: %q", got)
	}
}

func TestParsePacketErrors(t *testing.T) {
	for _, in := range []string{
		``,
		`7`,
		`x["a"]`,
		`2`,                       // event without payload
		`2[]`,                     // event without name
		`2[1]`,                    // event name not a string
		`2{"a":1}`,                // event payload not an array
		`2["a"`,                   // truncated JSON
		`2["a"]x`,                 // trailing garbage
		`1{}`,                     // disconnect with payload
		`0[]`,                     // connect payload not an object
		`4[]`,                     // connect error payload not an object or string
		`4`,                       // connect error without payload
		`3["a"]`,                  // ack without id
		`312`,                     // ack without payload
		`312{}`,                   // ack payload not an array
		`5["a"]`,                  // binary without attachment count
		`51["a"]`,                 // binary without '-'
		`5-["a"]`,                 // binary with empty count
		`599999-["a"]`,            // too many attachments
		`318446744073709551616[]`, // ack id overflows
		`399999999999999999999999[]`,
		"2[\"a\"]\xff",
	} {
		if p, err := ParsePacket([]byte(in)); err == nil {
			t.Errorf("ParsePacket(%q) = %+v, want error", in, p)
		} else if !errors.Is(err, ErrMalformed) {
			t.Errorf("ParsePacket(%q) error %v does not wrap ErrMalformed", in, err)
		}
	}
}

func TestSplitArray(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`[]`, nil},
		{` [ ] `, nil},
		{`["a"]`, []string{`"a"`}},
		{`["login",{"username":"u","password":"p,]}","token":""}]`, []string{`"login"`, `{"username":"u","password":"p,]}","token":""}`}},
		{`["a\"],",[1,[2,{"b":[3]}]],null,true,false,-1.5e3,"\\"]`, []string{`"a\"],"`, `[1,[2,{"b":[3]}]]`, `null`, `true`, `false`, `-1.5e3`, `"\\"`}},
		{"[ 1 ,\n2\t]", []string{`1`, `2`}},
	}
	for _, tc := range cases {
		got, err := SplitArray(nil, []byte(tc.in))
		if err != nil {
			t.Errorf("SplitArray(%q): %v", tc.in, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("SplitArray(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if string(got[i]) != tc.want[i] {
				t.Errorf("SplitArray(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
	for _, in := range []string{``, `{}`, `[`, `[1`, `[1,`, `[1 2]`, `["a]`, `[{]`, `[,]`, `[]x`, `[1]]`, `[:]`} {
		if got, err := SplitArray(nil, []byte(in)); err == nil {
			t.Errorf("SplitArray(%q) = %q, want error", in, got)
		}
	}
}

func TestEventName(t *testing.T) {
	for in, want := range map[string]string{`"login"`: "login", `"a\"b"`: `a"b`, `"é"`: "é", `""`: ""} {
		got, err := EventName(json.RawMessage(in))
		if err != nil || got != want {
			t.Errorf("EventName(%s) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{`1`, `"`, `"\x"`, ``} {
		if _, err := EventName(json.RawMessage(in)); err == nil {
			t.Errorf("EventName(%q): want error", in)
		}
	}
}

func TestPacketTypeString(t *testing.T) {
	if PacketBinaryAck.String() != "BINARY_ACK" || PacketType(9).String() != "PacketType(9)" {
		t.Fatal(PacketBinaryAck.String(), PacketType(9).String())
	}
}

func TestEncodeEvent(t *testing.T) {
	c := &Conn{}
	c.frames.New = newFrame
	f := c.frames.Get().(*frame)
	args := []any{
		map[string]string{"username": "u<&>", "password": "p", "token": ""},
		json.RawMessage(`{"raw" : [1, 2]}`),
		7,
		nil,
	}
	if err := f.encodeEvent(42, "login", args); err != nil {
		t.Fatal(err)
	}
	const want = `4242["login",{"password":"p","token":"","username":"u<&>"},{"raw":[1,2]},7,null]`
	if got := f.buf.String(); got != want {
		t.Fatalf("frame = %s\nwant    %s", got, want)
	}
	c.putFrame(f)

	f = c.frames.Get().(*frame)
	if f.buf.Len() != 0 {
		t.Fatal("pooled frame not reset")
	}
	if err := f.encodeEvent(0, "é\"\n", nil); err != nil || f.buf.String() != `420["é\"\n"]` {
		t.Fatalf("escaped event name: %s, %v", f.buf.String(), err)
	}
	f.buf.Reset()
	if err := f.encodeEvent(1, "x", []any{json.RawMessage(`{bad`)}); err == nil {
		t.Fatal("invalid json.RawMessage accepted")
	}
	f.buf.Reset()
	if err := f.encodeEvent(1, "x", []any{func() {}}); err == nil {
		t.Fatal("unsupported type accepted")
	}
}
