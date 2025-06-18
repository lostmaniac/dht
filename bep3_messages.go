package dht

import (
	"encoding/binary"
	"fmt"
	"io"
)

// BEP-3 Message IDs
const (
	MsgChoke         byte = 0
	MsgUnchoke       byte = 1
	MsgInterested    byte = 2
	MsgNotInterested byte = 3
	MsgHave          byte = 4
	MsgBitfield      byte = 5
	MsgRequest       byte = 6
	MsgPiece         byte = 7
	MsgCancel        byte = 8
	// MsgPort is also part of BEP-3 but less commonly used for basic data exchange,
	// and not requested in this subtask.
)

// Message represents a general peer wire protocol message.
// This is conceptual for now; ReadMessage returns msgID and payload directly.
type Message struct {
	ID      byte
	Payload []byte
}

// --- Message Creation (Serialization) Functions ---

// NewKeepAliveMsg creates a keep-alive message.
// Keep-alive messages are of length 0.
func NewKeepAliveMsg() []byte {
	return make([]byte, 4) // Length prefix of 0
}

// newPeerMessage creates a new message with a 4-byte length prefix and a 1-byte ID.
// payload can be nil for messages without a payload.
func newPeerMessage(id byte, payload []byte) []byte {
	length := uint32(1) // For message ID
	if payload != nil {
		length += uint32(len(payload))
	}
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], length)
	buf[4] = id
	if payload != nil {
		copy(buf[5:], payload)
	}
	return buf
}

// NewChokeMsg creates a Choke message.
func NewChokeMsg() []byte {
	return newPeerMessage(MsgChoke, nil)
}

// NewUnchokeMsg creates an Unchoke message.
func NewUnchokeMsg() []byte {
	return newPeerMessage(MsgUnchoke, nil)
}

// NewInterestedMsg creates an Interested message.
func NewInterestedMsg() []byte {
	return newPeerMessage(MsgInterested, nil)
}

// NewNotInterestedMsg creates a NotInterested message.
func NewNotInterestedMsg() []byte {
	return newPeerMessage(MsgNotInterested, nil)
}

// NewHaveMsg creates a Have message.
// Payload: <piece index (4 bytes)>
func NewHaveMsg(pieceIndex uint32) ([]byte, error) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, pieceIndex)
	return newPeerMessage(MsgHave, payload), nil
}

// NewBitfieldMsg creates a Bitfield message.
// Payload: <bitfield (variable length)>
func NewBitfieldMsg(bitfieldData []byte) ([]byte, error) {
	if bitfieldData == nil {
		// While an empty bitfield is possible, it might indicate an issue.
		// However, the protocol allows it.
		return newPeerMessage(MsgBitfield, []byte{}), nil
	}
	return newPeerMessage(MsgBitfield, bitfieldData), nil
}

// NewRequestMsg creates a Request message.
// Payload: <index (4 bytes)> <begin (4 bytes)> <length (4 bytes)>
func NewRequestMsg(index uint32, begin uint32, length uint32) ([]byte, error) {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], index)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	binary.BigEndian.PutUint32(payload[8:12], length)
	return newPeerMessage(MsgRequest, payload), nil
}

// NewCancelMsg creates a Cancel message.
// Payload: <index (4 bytes)> <begin (4 bytes)> <length (4 bytes)>
func NewCancelMsg(index uint32, begin uint32, length uint32) ([]byte, error) {
	payload := make([]byte, 12)
	binary.BigEndian.PutUint32(payload[0:4], index)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	binary.BigEndian.PutUint32(payload[8:12], length)
	return newPeerMessage(MsgCancel, payload), nil
}

// NewPieceMsg creates a Piece message.
// Payload: <index (4 bytes)> <begin (4 bytes)> <block (variable length)>
func NewPieceMsg(index uint32, begin uint32, blockData []byte) ([]byte, error) {
	if blockData == nil {
		// A piece message must contain a block, even if it's empty (though unusual).
		blockData = []byte{}
	}
	payload := make([]byte, 8+len(blockData))
	binary.BigEndian.PutUint32(payload[0:4], index)
	binary.BigEndian.PutUint32(payload[4:8], begin)
	copy(payload[8:], blockData)
	return newPeerMessage(MsgPiece, payload), nil
}

// --- Message Payload Parsing (Deserialization) Functions ---

// ParseHavePayload parses the payload of a Have message.
// Expected payload: <piece index (4 bytes)>
func ParseHavePayload(payload []byte) (pieceIndex uint32, err error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("have message payload must be 4 bytes, got %d", len(payload))
	}
	pieceIndex = binary.BigEndian.Uint32(payload)
	return pieceIndex, nil
}

// ParseRequestPayload parses the payload of a Request or Cancel message.
// Expected payload: <index (4 bytes)> <begin (4 bytes)> <length (4 bytes)>
func ParseRequestPayload(payload []byte) (index uint32, begin uint32, length uint32, err error) {
	if len(payload) != 12 {
		return 0, 0, 0, fmt.Errorf("request/cancel message payload must be 12 bytes, got %d", len(payload))
	}
	index = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	length = binary.BigEndian.Uint32(payload[8:12])
	return index, begin, length, nil
}

// ParsePiecePayload parses the payload of a Piece message.
// Expected payload: <index (4 bytes)> <begin (4 bytes)> <block (variable length)>
func ParsePiecePayload(payload []byte) (index uint32, begin uint32, block []byte, err error) {
	if len(payload) < 8 {
		return 0, 0, nil, fmt.Errorf("piece message payload must be at least 8 bytes, got %d", len(payload))
	}
	index = binary.BigEndian.Uint32(payload[0:4])
	begin = binary.BigEndian.Uint32(payload[4:8])
	block = payload[8:] // The rest of the payload is the block
	return index, begin, block, nil
}

// --- Generic Message Reading Function ---

const (
	// KeepAliveMsgID is a special identifier for keep-alive messages when returned by ReadMessage.
	// It's chosen to be distinct from actual BEP-3 message IDs.
	KeepAliveMsgID byte = 255
)

// ReadMessage reads a single peer wire message from an io.Reader (e.g., net.Conn).
// It returns a special KeepAliveMsgID for keep-alive messages (payload will be nil).
func ReadMessage(r io.Reader) (msgID byte, payload []byte, err error) {
	// 1. Read message length (4 bytes)
	lenBuf := make([]byte, 4)
	if _, err = io.ReadFull(r, lenBuf); err != nil {
		return 0, nil, err // Could be io.EOF or other error
	}
	msgLen := binary.BigEndian.Uint32(lenBuf)

	// 2. Handle Keep-Alive
	if msgLen == 0 {
		return KeepAliveMsgID, nil, nil
	}

	// 3. Read the message itself (ID + Payload)
	if msgLen > (1 << 24) { // Sanity check: 16MB message limit, arbitrary but practical
		return 0, nil, fmt.Errorf("message length %d exceeds reasonable limit", msgLen)
	}

	msgAndPayloadBuf := make([]byte, msgLen)
	if _, err = io.ReadFull(r, msgAndPayloadBuf); err != nil {
		return 0, nil, err // Could be io.EOF or other error
	}

	msgID = msgAndPayloadBuf[0]
	if msgLen > 1 {
		payload = msgAndPayloadBuf[1:]
	} else {
		payload = nil // No payload if length is 1 (only ID)
	}

	return msgID, payload, nil
}
