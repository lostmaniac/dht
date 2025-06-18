package dht

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"testing"
)

func TestNewKeepAliveMsg(t *testing.T) {
	msg := NewKeepAliveMsg()
	expected := []byte{0, 0, 0, 0}
	if !reflect.DeepEqual(msg, expected) {
		t.Errorf("NewKeepAliveMsg() = %v, want %v", msg, expected)
	}
}

func TestNewChokeMsg(t *testing.T) {
	msg := NewChokeMsg()
	// length prefix (1) + id (0)
	expectedPrefix := []byte{0, 0, 0, 1, MsgChoke}
	if !reflect.DeepEqual(msg, expectedPrefix) {
		t.Errorf("NewChokeMsg() = %v, want %v", msg, expectedPrefix)
	}
}

func TestNewUnchokeMsg(t *testing.T) {
	msg := NewUnchokeMsg()
	expectedPrefix := []byte{0, 0, 0, 1, MsgUnchoke}
	if !reflect.DeepEqual(msg, expectedPrefix) {
		t.Errorf("NewUnchokeMsg() = %v, want %v", msg, expectedPrefix)
	}
}

func TestNewInterestedMsg(t *testing.T) {
	msg := NewInterestedMsg()
	expectedPrefix := []byte{0, 0, 0, 1, MsgInterested}
	if !reflect.DeepEqual(msg, expectedPrefix) {
		t.Errorf("NewInterestedMsg() = %v, want %v", msg, expectedPrefix)
	}
}

func TestNewNotInterestedMsg(t *testing.T) {
	msg := NewNotInterestedMsg()
	expectedPrefix := []byte{0, 0, 0, 1, MsgNotInterested}
	if !reflect.DeepEqual(msg, expectedPrefix) {
		t.Errorf("NewNotInterestedMsg() = %v, want %v", msg, expectedPrefix)
	}
}

func TestNewHaveMsg(t *testing.T) {
	pieceIndex := uint32(123)
	msg, err := NewHaveMsg(pieceIndex)
	if err != nil {
		t.Fatalf("NewHaveMsg() error = %v", err)
	}

	// length prefix (1+4) + id (4) + payload (pieceIndex)
	expectedLength := uint32(5)
	if len(msg) != int(expectedLength+4) { // +4 for length prefix itself
		t.Errorf("NewHaveMsg() length = %d, want %d", len(msg), expectedLength+4)
	}
	if msg[4] != MsgHave {
		t.Errorf("NewHaveMsg() ID = %d, want %d", msg[4], MsgHave)
	}
	if binary.BigEndian.Uint32(msg[0:4]) != expectedLength {
		t.Errorf("NewHaveMsg() length prefix = %d, want %d", binary.BigEndian.Uint32(msg[0:4]), expectedLength)
	}
	if binary.BigEndian.Uint32(msg[5:]) != pieceIndex {
		t.Errorf("NewHaveMsg() piece index = %d, want %d", binary.BigEndian.Uint32(msg[5:]), pieceIndex)
	}
}

func TestNewBitfieldMsg(t *testing.T) {
	bitfieldData := []byte{0xAA, 0x55} // Example bitfield
	msg, err := NewBitfieldMsg(bitfieldData)
	if err != nil {
		t.Fatalf("NewBitfieldMsg() error = %v", err)
	}

	expectedLength := uint32(1 + len(bitfieldData))
	if msg[4] != MsgBitfield {
		t.Errorf("NewBitfieldMsg() ID = %d, want %d", msg[4], MsgBitfield)
	}
	if binary.BigEndian.Uint32(msg[0:4]) != expectedLength {
		t.Errorf("NewBitfieldMsg() length prefix = %d, want %d", binary.BigEndian.Uint32(msg[0:4]), expectedLength)
	}
	if !reflect.DeepEqual(msg[5:], bitfieldData) {
		t.Errorf("NewBitfieldMsg() bitfield data = %v, want %v", msg[5:], bitfieldData)
	}

	// Test with empty bitfield
	msgEmpty, _ := NewBitfieldMsg([]byte{})
	expectedLengthEmpty := uint32(1)
	if binary.BigEndian.Uint32(msgEmpty[0:4]) != expectedLengthEmpty {
		t.Errorf("NewBitfieldMsg() empty length prefix = %d, want %d", binary.BigEndian.Uint32(msgEmpty[0:4]), expectedLengthEmpty)
	}
	if len(msgEmpty) != 5 {
		t.Errorf("NewBitfieldMsg() empty total length = %d, want %d", len(msgEmpty), 5)
	}
}

func TestNewRequestMsg(t *testing.T) {
	index, begin, length := uint32(1), uint32(16384), uint32(16384)
	msg, err := NewRequestMsg(index, begin, length)
	if err != nil {
		t.Fatalf("NewRequestMsg() error = %v", err)
	}

	expectedMsgLength := uint32(1 + 12) // ID + 3*uint32
	if msg[4] != MsgRequest {
		t.Errorf("NewRequestMsg() ID = %d, want %d", msg[4], MsgRequest)
	}
	if binary.BigEndian.Uint32(msg[0:4]) != expectedMsgLength {
		t.Errorf("NewRequestMsg() length prefix = %d, want %d", binary.BigEndian.Uint32(msg[0:4]), expectedMsgLength)
	}
	parsedIndex := binary.BigEndian.Uint32(msg[5:9])
	parsedBegin := binary.BigEndian.Uint32(msg[9:13])
	parsedLength := binary.BigEndian.Uint32(msg[13:17])

	if parsedIndex != index || parsedBegin != begin || parsedLength != length {
		t.Errorf("NewRequestMsg() payload = (%d,%d,%d), want (%d,%d,%d)", parsedIndex, parsedBegin, parsedLength, index, begin, length)
	}
}

func TestNewCancelMsg(t *testing.T) {
	index, begin, length := uint32(2), uint32(0), uint32(16384)
	msg, err := NewCancelMsg(index, begin, length)
	if err != nil {
		t.Fatalf("NewCancelMsg() error = %v", err)
	}
	expectedMsgLength := uint32(1 + 12)
	if msg[4] != MsgCancel {
		t.Errorf("NewCancelMsg() ID = %d, want %d", msg[4], MsgCancel)
	}
	// Similar payload check as Request
}

func TestNewPieceMsg(t *testing.T) {
	index, begin := uint32(3), uint32(32768)
	blockData := make([]byte, 100)
	for i := range blockData {
		blockData[i] = byte(i)
	}
	msg, err := NewPieceMsg(index, begin, blockData)
	if err != nil {
		t.Fatalf("NewPieceMsg() error = %v", err)
	}

	expectedMsgLength := uint32(1 + 8 + len(blockData)) // ID + index + begin + block
	if msg[4] != MsgPiece {
		t.Errorf("NewPieceMsg() ID = %d, want %d", msg[4], MsgPiece)
	}
	if binary.BigEndian.Uint32(msg[0:4]) != expectedMsgLength {
		t.Errorf("NewPieceMsg() length prefix = %d, want %d", binary.BigEndian.Uint32(msg[0:4]), expectedMsgLength)
	}
	parsedIndex := binary.BigEndian.Uint32(msg[5:9])
	parsedBegin := binary.BigEndian.Uint32(msg[9:13])
	parsedBlock := msg[13:]

	if parsedIndex != index || parsedBegin != begin || !reflect.DeepEqual(parsedBlock, blockData) {
		t.Errorf("NewPieceMsg() payload mismatch")
	}
}

// --- Payload Parsing Tests ---

func TestParseHavePayload(t *testing.T) {
	validPayload := make([]byte, 4)
	binary.BigEndian.PutUint32(validPayload, 42)
	idx, err := ParseHavePayload(validPayload)
	if err != nil {
		t.Fatalf("ParseHavePayload(valid) error = %v", err)
	}
	if idx != 42 {
		t.Errorf("ParseHavePayload(valid) index = %d, want %d", idx, 42)
	}

	_, err = ParseHavePayload([]byte{1, 2, 3}) // Malformed
	if err == nil {
		t.Error("ParseHavePayload(malformed) expected error, got nil")
	}
}

func TestParseRequestPayload(t *testing.T) {
	validPayload := make([]byte, 12)
	binary.BigEndian.PutUint32(validPayload[0:4], 1) // index
	binary.BigEndian.PutUint32(validPayload[4:8], 2) // begin
	binary.BigEndian.PutUint32(validPayload[8:12], 3) // length

	idx, bgn, ln, err := ParseRequestPayload(validPayload)
	if err != nil {
		t.Fatalf("ParseRequestPayload(valid) error = %v", err)
	}
	if idx != 1 || bgn != 2 || ln != 3 {
		t.Errorf("ParseRequestPayload(valid) got (%d,%d,%d), want (1,2,3)", idx, bgn, ln)
	}

	_, _, _, err = ParseRequestPayload(make([]byte, 11)) // Malformed
	if err == nil {
		t.Error("ParseRequestPayload(malformed) expected error, got nil")
	}
}

func TestParsePiecePayload(t *testing.T) {
	blockData := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	validPayload := make([]byte, 8+len(blockData))
	binary.BigEndian.PutUint32(validPayload[0:4], 7) // index
	binary.BigEndian.PutUint32(validPayload[4:8], 8) // begin
	copy(validPayload[8:], blockData)

	idx, bgn, blk, err := ParsePiecePayload(validPayload)
	if err != nil {
		t.Fatalf("ParsePiecePayload(valid) error = %v", err)
	}
	if idx != 7 || bgn != 8 || !reflect.DeepEqual(blk, blockData) {
		t.Errorf("ParsePiecePayload(valid) mismatch")
	}

	_, _, _, err = ParsePiecePayload(make([]byte, 7)) // Malformed (too short for header)
	if err == nil {
		t.Error("ParsePiecePayload(malformed header) expected error, got nil")
	}
}

// --- ReadMessage Tests ---

func TestReadMessage_KeepAlive(t *testing.T) {
	buf := bytes.NewBuffer(NewKeepAliveMsg())
	id, payload, err := ReadMessage(buf)
	if err != nil {
		t.Fatalf("ReadMessage(KeepAlive) error = %v", err)
	}
	if id != KeepAliveMsgID {
		t.Errorf("ReadMessage(KeepAlive) ID = %d, want %d (KeepAliveMsgID)", id, KeepAliveMsgID)
	}
	if payload != nil {
		t.Errorf("ReadMessage(KeepAlive) payload = %v, want nil", payload)
	}
}

func TestReadMessage_SingleMessage(t *testing.T) {
	chokeMsg := NewChokeMsg()
	buf := bytes.NewBuffer(chokeMsg)
	id, payload, err := ReadMessage(buf)
	if err != nil {
		t.Fatalf("ReadMessage(Choke) error = %v", err)
	}
	if id != MsgChoke {
		t.Errorf("ReadMessage(Choke) ID = %d, want %d", id, MsgChoke)
	}
	if len(payload) != 0 { // Choke has no payload beyond ID
		t.Errorf("ReadMessage(Choke) payload len = %d, want 0", len(payload))
	}
}

func TestReadMessage_HaveMessage(t *testing.T) {
	haveMsg, _ := NewHaveMsg(99)
	buf := bytes.NewBuffer(haveMsg)
	id, payload, err := ReadMessage(buf)
	if err != nil {
		t.Fatalf("ReadMessage(Have) error = %v", err)
	}
	if id != MsgHave {
		t.Errorf("ReadMessage(Have) ID = %d, want %d", id, MsgHave)
	}
	if len(payload) != 4 {
		t.Errorf("ReadMessage(Have) payload len = %d, want 4", len(payload))
	}
	parsedIdx, _ := ParseHavePayload(payload)
	if parsedIdx != 99 {
		t.Errorf("ReadMessage(Have) parsed index = %d, want 99", parsedIdx)
	}
}

func TestReadMessage_Sequence(t *testing.T) {
	var data []byte
	data = append(data, NewChokeMsg()...)
	data = append(data, NewInterestedMsg()...)
	haveMsg, _ := NewHaveMsg(101)
	data = append(data, haveMsg...)
	buf := bytes.NewBuffer(data)

	// Read Choke
	id, _, err := ReadMessage(buf)
	if err != nil || id != MsgChoke {
		t.Fatalf("ReadMessage(Sequence) Choke failed: id=%d, err=%v", id, err)
	}
	// Read Interested
	id, _, err = ReadMessage(buf)
	if err != nil || id != MsgInterested {
		t.Fatalf("ReadMessage(Sequence) Interested failed: id=%d, err=%v", id, err)
	}
	// Read Have
	id, payload, err := ReadMessage(buf)
	if err != nil || id != MsgHave {
		t.Fatalf("ReadMessage(Sequence) Have failed: id=%d, err=%v", id, err)
	}
	parsedIdx, _ := ParseHavePayload(payload)
	if parsedIdx != 101 {
		t.Errorf("ReadMessage(Sequence) Have index = %d, want 101", parsedIdx)
	}
}

func TestReadMessage_EOF_DuringLength(t *testing.T) {
	buf := bytes.NewBuffer([]byte{0, 0}) // Incomplete length
	_, _, err := ReadMessage(buf)
	if err != io.EOF && err != io.ErrUnexpectedEOF { // Behavior can vary slightly
		t.Errorf("ReadMessage(EOF length) expected EOF/ErrUnexpectedEOF, got %v", err)
	}
}

func TestReadMessage_EOF_DuringPayload(t *testing.T) {
	chokeMsg := NewChokeMsg() // Length 1, ID 0
	// Intentionally corrupt by shortening the actual message after length prefix
	corruptedMsg := append(chokeMsg[0:4], chokeMsg[4]) // Length prefix + ID, but no more bytes if length > 1

	// To test EOF during payload, we need a message with payload.
	haveMsgPartialPayload, _ := NewHaveMsg(100) // This is len=5, id=4, payload=4bytes
	// Simulate only length prefix and ID, and 1 byte of payload
	partialData := append(haveMsgPartialPayload[0:4], haveMsgPartialPayload[4:6]...)

	buf := bytes.NewBuffer(partialData)
	_, _, err := ReadMessage(buf)
	if err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Errorf("ReadMessage(EOF payload) expected EOF/ErrUnexpectedEOF, got %v", err)
	}
}


func TestReadMessage_InvalidLength_TooLarge(t *testing.T) {
	// Create a message with an excessively large length prefix
	lenBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lenBuf, (1<<25)) // 32MB, larger than our sanity check in ReadMessage
	buf := bytes.NewBuffer(lenBuf)
	_, _, err := ReadMessage(buf)
	if err == nil {
		t.Error("ReadMessage(TooLarge) expected error for oversized message, got nil")
	}
	// Check if the error message contains "exceeds reasonable limit" or similar
	// This depends on the exact error string in ReadMessage.
	// For now, just checking err != nil is okay.
}
