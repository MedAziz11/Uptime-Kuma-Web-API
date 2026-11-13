// Package sio is a minimal Socket.IO client: the Engine.IO protocol 4 and
// Socket.IO protocol 5 packet codec, and a client that speaks them over a
// single WebSocket.
//
// It knows nothing about Uptime Kuma. It implements what a client of a
// Socket.IO 4.x server needs to call events and receive pushes:
//
//   - WebSocket transport only (EIO=4&transport=websocket), no long-polling
//     and therefore no upgrade.
//   - The Engine.IO open handshake; pings from the server are answered with
//     pongs, and the connection is closed when no ping arrives within
//     pingInterval + pingTimeout.
//   - The default namespace only ("/"): CONNECT, CONNECT_ERROR, DISCONNECT,
//     EVENT (with or without an ack id) and ACK.
//
// # Binary packets
//
// BINARY_EVENT and BINARY_ACK (packet types 5 and 6) are parsed by the codec,
// so their header (attachment count, namespace, ack id) is validated, but the
// client never sends them and does not reassemble them: a received binary
// event is dropped together with its attachment frames, and a binary ack
// fails the matching call with ErrBinaryUnsupported. The connection stays
// usable. Uptime Kuma's management events carry JSON only.
//
// # Events sent by the server with an ack id
//
// The server may ask the client to acknowledge an event. The client does not
// answer such requests; the event is delivered like any other.
package sio
