package sio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Engine.IO protocol 4 packet types, as the first byte of a text frame.
const (
	engineOpen    = '0'
	engineClose   = '1'
	enginePing    = '2'
	enginePong    = '3'
	engineMessage = '4'
	engineUpgrade = '5'
	engineNoop    = '6'
)

// PacketType is a Socket.IO protocol 5 packet type.
type PacketType uint8

// Socket.IO protocol 5 packet types.
const (
	PacketConnect PacketType = iota
	PacketDisconnect
	PacketEvent
	PacketAck
	PacketConnectError
	PacketBinaryEvent
	PacketBinaryAck
)

var packetTypeNames = [...]string{"CONNECT", "DISCONNECT", "EVENT", "ACK", "CONNECT_ERROR", "BINARY_EVENT", "BINARY_ACK"}

func (t PacketType) String() string {
	if int(t) < len(packetTypeNames) {
		return packetTypeNames[t]
	}
	return "PacketType(" + strconv.Itoa(int(t)) + ")"
}

// maxAttachments bounds the attachment count of a binary packet, so that a
// malformed header cannot make the client discard frames for ever.
const maxAttachments = 1024

// ErrMalformed is wrapped by every error that reports an invalid packet.
var ErrMalformed = errors.New("sio: malformed packet")

// Packet is a decoded Socket.IO packet (without the Engine.IO "4" prefix).
type Packet struct {
	Type PacketType
	// Namespace is empty for the default namespace "/".
	Namespace string
	// Attachments is the number of binary attachments (PacketBinaryEvent and
	// PacketBinaryAck only).
	Attachments int
	// ID is the ack id; it is meaningful only when HasID is true.
	ID    uint64
	HasID bool
	// Data is the JSON payload, possibly empty. ParsePacket returns a slice
	// of its input, not a copy.
	Data []byte
}

// ParsePacket decodes a Socket.IO packet. The payload is checked to be valid
// JSON of the kind the packet type requires (protocol 5, section "Packet
// format"): CONNECT carries an optional object, DISCONNECT nothing,
// CONNECT_ERROR an object or a string, EVENT a non-empty array starting with
// the event name, ACK an array and an ack id. It never panics.
func ParsePacket(b []byte) (Packet, error) {
	var p Packet
	if len(b) == 0 {
		return p, fmt.Errorf("%w: empty", ErrMalformed)
	}
	if b[0] < '0' || b[0] > '6' {
		return p, fmt.Errorf("%w: unknown packet type %q", ErrMalformed, b[0])
	}
	p.Type = PacketType(b[0] - '0')
	i := 1

	if p.Type == PacketBinaryEvent || p.Type == PacketBinaryAck {
		n, j, ok := parseUint(b, i)
		if !ok || j == i || j >= len(b) || b[j] != '-' {
			return p, fmt.Errorf("%w: bad attachment count", ErrMalformed)
		}
		if n > maxAttachments {
			return p, fmt.Errorf("%w: %d attachments", ErrMalformed, n)
		}
		p.Attachments = int(n)
		i = j + 1
	}

	if i < len(b) && b[i] == '/' {
		end := len(b)
		next := len(b)
		if k := bytes.IndexByte(b[i:], ','); k >= 0 {
			end = i + k
			next = end + 1
		}
		if ns := b[i:end]; len(ns) != 1 {
			p.Namespace = string(ns)
		}
		i = next
	}

	if i < len(b) && isDigit(b[i]) {
		id, j, ok := parseUint(b, i)
		if !ok {
			return p, fmt.Errorf("%w: ack id overflows", ErrMalformed)
		}
		p.ID, p.HasID = id, true
		i = j
	}

	if i < len(b) {
		p.Data = b[i:]
	}
	if err := p.validate(); err != nil {
		return Packet{}, err
	}
	return p, nil
}

func (p *Packet) validate() error {
	if len(p.Data) > 0 && !json.Valid(p.Data) {
		return fmt.Errorf("%w: %v payload is not valid JSON", ErrMalformed, p.Type)
	}
	kind := firstByte(p.Data)
	ok := true
	switch p.Type {
	case PacketConnect:
		ok = kind == 0 || kind == '{'
	case PacketDisconnect:
		ok = kind == 0
	case PacketConnectError:
		ok = kind == '{' || kind == '"'
	case PacketEvent, PacketBinaryEvent:
		ok = kind == '[' && firstByte(p.Data[skipSpace(p.Data, 0)+1:]) == '"'
	case PacketAck, PacketBinaryAck:
		ok = kind == '[' && p.HasID
	}
	if !ok {
		return fmt.Errorf("%w: invalid %v payload", ErrMalformed, p.Type)
	}
	return nil
}

// AppendPacket appends the encoding of p to dst and returns the extended
// buffer. p.Data is copied as is; it must be valid JSON of the right kind.
func AppendPacket(dst []byte, p Packet) []byte {
	dst = append(dst, byte('0'+p.Type))
	if p.Type == PacketBinaryEvent || p.Type == PacketBinaryAck {
		dst = strconv.AppendInt(dst, int64(p.Attachments), 10)
		dst = append(dst, '-')
	}
	if p.Namespace != "" && p.Namespace != "/" {
		dst = append(dst, p.Namespace...)
		dst = append(dst, ',')
	}
	if p.HasID {
		dst = strconv.AppendUint(dst, p.ID, 10)
	}
	return append(dst, p.Data...)
}

// SplitArray appends the elements of the JSON array data to dst, as slices of
// data, and returns the extended slice. It checks the structure it walks but
// is not a full validator: give it payloads that passed ParsePacket.
func SplitArray(dst []json.RawMessage, data []byte) ([]json.RawMessage, error) {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '[' {
		return dst, fmt.Errorf("%w: not an array", ErrMalformed)
	}
	i = skipSpace(data, i+1)
	if i < len(data) && data[i] == ']' {
		return dst, trailing(data, i+1)
	}
	for {
		end, err := skipValue(data, i)
		if err != nil {
			return dst, err
		}
		dst = append(dst, data[i:end])
		i = skipSpace(data, end)
		if i >= len(data) {
			return dst, fmt.Errorf("%w: unterminated array", ErrMalformed)
		}
		switch data[i] {
		case ',':
			i = skipSpace(data, i+1)
		case ']':
			return dst, trailing(data, i+1)
		default:
			return dst, fmt.Errorf("%w: unexpected %q in array", ErrMalformed, data[i])
		}
	}
}

// EventName decodes a JSON string (the first element of an EVENT payload).
func EventName(raw json.RawMessage) (string, error) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
		return string(raw[1 : len(raw)-1]), nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: event name: %w", ErrMalformed, err)
	}
	return s, nil
}

func trailing(data []byte, i int) error {
	if skipSpace(data, i) != len(data) {
		return fmt.Errorf("%w: data after array", ErrMalformed)
	}
	return nil
}

// skipValue returns the index just after the JSON value starting at i.
func skipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, fmt.Errorf("%w: missing value", ErrMalformed)
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				k, err := skipString(b, j)
				if err != nil {
					return 0, err
				}
				j = k - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, fmt.Errorf("%w: unterminated value", ErrMalformed)
	case ',', ']', '}', ':':
		return 0, fmt.Errorf("%w: unexpected %q", ErrMalformed, b[i])
	default:
		j := i
		for j < len(b) && !isDelim(b[j]) {
			j++
		}
		return j, nil
	}
}

func skipString(b []byte, i int) (int, error) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("%w: unterminated string", ErrMalformed)
}

// parseUint reads decimal digits from b[i:]; ok is false on overflow.
func parseUint(b []byte, i int) (n uint64, end int, ok bool) {
	const cutoff = (1<<64 - 1) / 10
	j := i
	for ; j < len(b) && isDigit(b[j]); j++ {
		d := uint64(b[j] - '0')
		if n > cutoff || n*10 > 1<<64-1-d {
			return 0, j, false
		}
		n = n*10 + d
	}
	return n, j, true
}

func firstByte(b []byte) byte {
	if i := skipSpace(b, 0); i < len(b) {
		return b[i]
	}
	return 0
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && isSpace(b[i]) {
		i++
	}
	return i
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isDelim(c byte) bool { return isSpace(c) || c == ',' || c == ']' || c == '}' || c == ':' }
