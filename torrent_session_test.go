package dht

import (
	"encoding/hex"
	"testing"
	"time"
	"fmt"
	"crypto/sha1"
)

// MockPieceManager for TorrentSession tests
type MockPieceManager struct {
	PieceManager // Embed real PieceManager to avoid reimplementing everything
	getBlockData func(pieceIndex uint32, beginOffset uint32, length uint32) ([]byte, error)
	havePiece    func(pieceIndex uint32) bool
}

func (mpm *MockPieceManager) GetBlockData(pieceIndex uint32, beginOffset uint32, length uint32) ([]byte, error) {
	if mpm.getBlockData != nil {
		return mpm.getBlockData(pieceIndex, beginOffset, length)
	}
	// Fallback or default behavior if not set
	return nil, fmt.Errorf("GetBlockData not mocked")
}

func (mpm *MockPieceManager) HavePiece(pieceIndex uint32) bool {
	if mpm.havePiece != nil {
		return mpm.havePiece(pieceIndex)
	}
	return false
}

// Helper to create a dummy PieceManager for tests that don't focus on its internals
func newTestPieceManager(numPieces int, pieceLength uint32, totalLength uint64) *PieceManager {
	hashes := make([][]byte, numPieces)
	for i := 0; i < numPieces; i++ {
		h := sha1.Sum([]byte(fmt.Sprintf("piece%d", i)))
		hashes[i] = h[:]
	}
	pm, _ := NewPieceManager(uint32(numPieces), pieceLength, totalLength, hashes)
	return pm
}


func TestNewTorrentSession(t *testing.T) {
	cfg := DefaultTorrentSessionConfig()
	infoHash := []byte("testinfohash12345678")
	peerID := []byte("-TEST001-selfpeerid12")
	dhtNode := New(nil) // Placeholder DHT node
	defer dhtNode.Stop() // Assuming DHT has a Stop method or similar cleanup

	pm := newTestPieceManager(10, BlockSize, uint64(10*BlockSize))

	ts := NewTorrentSession(infoHash, peerID, dhtNode, pm, cfg)

	if ts == nil {
		t.Fatal("NewTorrentSession returned nil")
	}
	if string(ts.infoHash) != string(infoHash) {
		t.Errorf("ts.infoHash = %s, want %s", ts.infoHash, infoHash)
	}
	if string(ts.ourPeerID) != string(peerID) {
		t.Errorf("ts.ourPeerID = %s, want %s", ts.ourPeerID, peerID)
	}
	if ts.config.MaxPeers != cfg.MaxPeers {
		t.Errorf("ts.config.MaxPeers = %d, want %d", ts.config.MaxPeers, cfg.MaxPeers)
	}
	if ts.pieceManager == nil {
		t.Error("ts.pieceManager is nil")
	}
}

func TestTorrentSession_HandlePeerMessage_Interest(t *testing.T) {
	cfg := DefaultTorrentSessionConfig()
	ts := NewTorrentSession([]byte("testhash"), []byte("testselfid"), nil, newTestPieceManager(1, BlockSize, BlockSize), cfg)

	// Simulate a peer session
	peerAddr := "1.2.3.4:5678"
	ps := NewPeerSession(peerAddr, ts.infoHash, ts.ourPeerID, ts.peerMessages)
	ts.peerSessions[peerAddr] = ps // Manually add to map for this test

	if ps.isInterested != false {
		t.Fatalf("PeerSession initial isInterested state incorrect")
	}

	// Test Interested message
	ts.handlePeerMessage(InboundPeerMessage{
		PeerAddr: peerAddr,
		Message:  &PeerMessage{ID: MsgInterested, Payload: nil},
	})
	if !ps.isInterested {
		t.Errorf("After MsgInterested, peerSession.isInterested = false, want true")
	}

	// Test NotInterested message
	ts.handlePeerMessage(InboundPeerMessage{
		PeerAddr: peerAddr,
		Message:  &PeerMessage{ID: MsgNotInterested, Payload: nil},
	})
	if ps.isInterested {
		t.Errorf("After MsgNotInterested, peerSession.isInterested = true, want false")
	}
}

func TestTorrentSession_HandleRequestMessage(t *testing.T) {
	cfg := DefaultTorrentSessionConfig()
	mockPM := &MockPieceManager{}
	ts := NewTorrentSession([]byte("requesthash"), []byte("requestselfid"), nil, &mockPM.PieceManager, cfg) // Use the embedded real PM for other functions

	peerAddr := "peer.request.com:1234"
	ps := NewPeerSession(peerAddr, ts.infoHash, ts.ourPeerID, ts.peerMessages)
	ts.peerSessions[peerAddr] = ps

	requestIndex, requestBegin, requestLength := uint32(0), uint32(0), uint32(BlockSize)
	requestPayload, _ := NewRequestMsg(requestIndex, requestBegin, requestLength) // Use NewRequestMsg to get payload
	rawRequestPayload := requestPayload[5:] // Strip length prefix and ID

	// Case 1: Peer is choked
	ps.amChoking = true // We are choking this peer
	ts.handleRequestMessage(peerAddr, rawRequestPayload)
	select {
	case <-ps.outgoingMessageQueue:
		t.Error("handleRequestMessage: Sent PIECE message when peer is choked")
	default: // Expected: no message
	}

	// Case 2: Peer is unchoked, we don't have the piece
	ps.amChoking = false
	mockPM.havePiece = func(pieceIndex uint32) bool { return false }
	ts.handleRequestMessage(peerAddr, rawRequestPayload)
	select {
	case <-ps.outgoingMessageQueue:
		t.Error("handleRequestMessage: Sent PIECE message when piece not available")
	default: // Expected: no message
	}

	// Case 3: Peer is unchoked, we have piece, GetBlockData returns error
	mockPM.havePiece = func(pieceIndex uint32) bool { return true }
	mockPM.getBlockData = func(pi uint32, bo uint32, l uint32) ([]byte, error) {
		return nil, fmt.Errorf("mock GetBlockData error")
	}
	ts.handleRequestMessage(peerAddr, rawRequestPayload)
	select {
	case <-ps.outgoingMessageQueue:
		t.Error("handleRequestMessage: Sent PIECE message when GetBlockData failed")
	default: // Expected: no message
	}

	// Case 4: Success
	expectedBlockData := make([]byte, requestLength)
	for i := range expectedBlockData { expectedBlockData[i] = byte(i) }
	mockPM.getBlockData = func(pi uint32, bo uint32, l uint32) ([]byte, error) {
		if pi == requestIndex && bo == requestBegin && l == requestLength {
			return expectedBlockData, nil
		}
		return nil, fmt.Errorf("unexpected GetBlockData call")
	}

	ts.handleRequestMessage(peerAddr, rawRequestPayload)
	select {
	case sentMsgBytes := <-ps.outgoingMessageQueue:
		if len(sentMsgBytes) < 5 {
			t.Fatal("Sent message too short")
		}
		if sentMsgBytes[4] != MsgPiece {
			t.Errorf("handleRequestMessage: Expected PIECE message (ID %d), got ID %d", MsgPiece, sentMsgBytes[4])
		}
		// Further parsing can be done here if needed, like in bep3_messages_test
		_, _, sentBlock, err := ParsePiecePayload(sentMsgBytes[5:])
		if err != nil {
			t.Fatalf("Error parsing sent PIECE message payload: %v", err)
		}
		if !bytes.Equal(sentBlock, expectedBlockData) {
			t.Error("Sent PIECE message block data mismatch")
		}
	case <-time.After(100 * time.Millisecond): // Timeout if no message sent
		t.Error("handleRequestMessage: Did not send PIECE message on success")
	}
}


func TestTorrentSession_ManageChokingPeers(t *testing.T) {
	cfg := DefaultTorrentSessionConfig()
	cfg.MaxUploadSlots = 2
	ts := NewTorrentSession([]byte("chokehashing"), []byte("chokeselfid1"), nil, newTestPieceManager(1,BlockSize,BlockSize), cfg)

	// Create 4 peers
	peers := make([]*PeerSession, 4)
	for i := 0; i < 4; i++ {
		addr := fmt.Sprintf("peer%d.choke.com:111%d", i, i)
		peers[i] = NewPeerSession(addr, ts.infoHash, ts.ourPeerID, ts.peerMessages)
		ts.peerSessions[addr] = peers[i]
		// All start as amChoking=true, isInterested=false
	}

	// Scenario 1: No one interested, no one should be unchoked
	ts.manageChokingPeers()
	for i, p := range peers {
		if !p.amChoking {
			t.Errorf("Peer %d unchoked when not interested", i)
		}
	}

	// Scenario 2: 3 peers interested, 2 slots. Should unchoke 2.
	peers[0].isInterested = true
	peers[1].isInterested = true
	peers[2].isInterested = true
	// peers[3] remains not interested

	ts.manageChokingPeers()

	unchokedCount := 0
	for _, p := range peers {
		if !p.amChoking {
			unchokedCount++
		}
	}
	if unchokedCount != 2 {
		t.Errorf("manageChokingPeers: unchokedCount = %d, want 2", unchokedCount)
	}
	// Check that the unchoked peers are from the interested ones
	if peers[0].amChoking && peers[1].amChoking && peers[2].amChoking {
		// This could happen if map iteration order was unfavorable.
		// A better test would check specific peers if ranking was implemented.
		// For now, just ensure *some* two interested peers were unchoked.
		t.Logf("manageChokingPeers: Note - specific peers unchoked depends on map iteration. Got: p0 choked:%v, p1 choked:%v, p2 choked:%v", peers[0].amChoking, peers[1].amChoking, peers[2].amChoking)
	}
	if !peers[3].amChoking { // Peer 3 should remain choked (not interested)
		t.Errorf("Peer 3 (not interested) was unchoked")
	}


	// Scenario 3: One previously unchoked peer becomes not interested.
	// Assume peers[0] and peers[1] were unchoked. peers[0] becomes not interested.
	// peers[2] is still interested and choked.
	// Result: peers[0] should be choked. peers[2] should be unchoked. peers[1] remains unchoked.

	// Reset states for clarity for this scenario
	for _,p := range peers { p.amChoking = true } // Choke all first
	peers[0].isInterested = false // Was interested, now not
	peers[1].isInterested = true  // Stays interested
	peers[2].isInterested = true  // Stays interested
	peers[3].isInterested = false // Stays not interested

	// Simulate peers[1] and a random other interested peer (say peers[2]) were unchoked in a previous round
	// Or, let's set it up so manageChokingPeers has to make a change.
	// Say, peers[0] was unchoked (but is now not interested) and peers[1] was unchoked (and is interested)
	peers[0].amChoking = false // Unchoked, but no longer interested
	peers[1].amChoking = false // Unchoked and interested
	peers[2].amChoking = true  // Choked and interested

	ts.manageChokingPeers()

	if peers[0].amChoking == false {
		t.Errorf("Peer 0 should be choked (became not interested)")
	}
	if peers[1].amChoking == true && peers[2].amChoking == true {
		// At least one of them should be unchoked to fill the slot from peer 0
		t.Errorf("Neither peer 1 nor peer 2 were unchoked to fill peer 0's slot.")
	}
	if !peers[1].amChoking && !peers[2].amChoking && unchokedCountAfterScenario3(peers) > 2 {
		t.Errorf("More than 2 peers unchoked after peer 0 became not interested.")
	}

	finalUnchoked := 0
	for _, p := range peers {
		if !p.amChoking {
			finalUnchoked++
			if !p.isInterested {
				t.Errorf("Peer %s is unchoked but not interested", p.addr)
			}
		}
	}
	if finalUnchoked > cfg.MaxUploadSlots {
		t.Errorf("Final unchoked count %d exceeds MaxUploadSlots %d", finalUnchoked, cfg.MaxUploadSlots)
	}
	if finalUnchoked < cfg.MaxUploadSlots && (peers[1].isInterested && peers[1].amChoking || peers[2].isInterested && peers[2].amChoking){
		// If slots are free and interested peers are choked, it's an issue
		t.Errorf("Slots available (%d/%d) but interested peers remain choked.", finalUnchoked, cfg.MaxUploadSlots)
	}
}

func unchokedCountAfterScenario3(peers []*PeerSession) int {
	c := 0
	for _, p := range peers {
		if !p.amChoking {
			c++
		}
	}
	return c
}

// Note: To properly test which specific peers are choked/unchoked when choices exist (e.g. > MaxUploadSlots interested peers),
// the choking algorithm would need to be deterministic (e.g. sort by some metric).
// Current tests primarily check counts and states of not-interested peers.
// Also, checking ps.outgoingMessageQueue for CHOKE/UNCHOKE messages would make these tests more robust.
// For now, we trust that SendChokeMsg/SendUnchokeMsg are called when amChoking state changes.

func TestTorrentSession_StartStop(t *testing.T) {
	cfg := DefaultTorrentSessionConfig()
	// Mock DHT that does nothing for Start/Stop test
	mockDHT := New(NewCrawlConfig()) // Use a config that doesn't auto-connect
	go func() {
		// Need to consume from mockDHT.packets to prevent blocking if any messages were sent
		// This is a very basic way to prevent dht.Run() from blocking indefinitely in this test.
		// A proper mock DHT would be better.
		if mockDHT != nil && mockDHT.packets != nil {
			for range mockDHT.packets {}
		}
	}()


	ts := NewTorrentSession([]byte("startstophash"), []byte("startstopselfid"), mockDHT, newTestPieceManager(1,BlockSize,BlockSize), cfg)

	// Quick Start and Stop to check for deadlocks or basic errors.
	// More advanced tests would require mocking DHT peer responses.
	ts.Start()

	// Give it a moment to run discoverPeers, etc.
	time.Sleep(100 * time.Millisecond)

	ts.Stop() // This should close ts.stop and wait for mainLoop

	// If Stop() completed without deadlock, the test is largely successful for this scope.
	// Add more assertions if specific states need to be checked after stop.

	// Try to ensure the DHT's deregister was called (if it were a mock dht, we could check that)
	// For now, just ensure it completes.
}
