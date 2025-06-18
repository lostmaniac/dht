package dht

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"
	// "github.com/lostmaniac/dht/bep3_messages" // If it were a separate package
	// "github.com/lostmaniac/dht/piece_manager" // If it were a separate package
)

// MetainfoFile placeholder for now
type MetainfoFile struct {
	InfoHash []byte
	Name     string
	Length   uint64
	// Other fields like PieceHashes, PieceLength would be here
}

// InboundPeerMessage wraps a message received from a peer with the peer's address.
type InboundPeerMessage struct {
	PeerAddr string
	Message  *PeerMessage // Using the conceptual PeerMessage {ID byte; Payload []byte}
}

// TorrentSessionConfig holds configuration for a TorrentSession.
type TorrentSessionConfig struct {
	MaxPeers          int           // Maximum number of active peer connections
	TargetPeers       int           // Desired number of active peer connections
	ConnectTimeout    time.Duration // Timeout for establishing a connection to a peer (used by PeerSession)
	HandshakeTimeout  time.Duration // Timeout for the BitTorrent handshake (used by PeerSession)
	ReadTimeout       time.Duration // Timeout for reading messages from a peer (used by PeerSession)
	WriteTimeout      time.Duration // Timeout for writing messages to a peer (used by PeerSession)
	KeepAliveInterval time.Duration // How often PeerSession should send keep-alive if no other writes.
	DefaultPieceTimeout time.Duration // Timeout for receiving a piece/block (TODO: implement in download logic)
	MaxUploadSlots    int           // Maximum number of peers to unchoke for uploading
}

// DefaultTorrentSessionConfig returns a configuration with default values.
func DefaultTorrentSessionConfig() TorrentSessionConfig {
	return TorrentSessionConfig{
		MaxPeers:          50,
		TargetPeers:       20, // Reduced target peers for more churn/testing
		ConnectTimeout:    ConnectTimeout,    // Use PeerSession's constant
		HandshakeTimeout:  HandshakeTimeout,  // Use PeerSession's constant
		ReadTimeout:       ReadTimeout,       // Use PeerSession's constant
		WriteTimeout:      WriteTimeout,      // Use PeerSession's constant
		KeepAliveInterval: PeerKeepAliveInterval, // Use PeerSession's constant
		DefaultPieceTimeout: 30 * time.Second,
		MaxUploadSlots:    4,
	}
}

// TorrentSession manages the download and upload of a single torrent.
type TorrentSession struct {
	config         TorrentSessionConfig
	infoHash       []byte
	ourPeerID      []byte
	metainfo       *MetainfoFile             // Placeholder for actual metainfo
	pieceManager   *PieceManager
	dhtNode        *DHT
	peerSessions   map[string]*PeerSession   // Active peer sessions: peerAddr -> PeerSession
	pendingPeers   map[string]bool           // Peers we are currently trying to connect to: peerAddr -> true
	newPeersFromDHT chan []*Peer
	peerMessages   chan InboundPeerMessage   // Receives messages from all active PeerSessions
	stop           chan struct{}
	wg             sync.WaitGroup
	connMx         sync.RWMutex
}

// NewTorrentSession creates a new torrent session.
func NewTorrentSession(infoHash []byte, ourPeerID []byte, dhtNode *DHT, pieceManager *PieceManager, cfg TorrentSessionConfig) *TorrentSession {
	if pieceManager == nil {
		panic("PieceManager cannot be nil")
	}

	ts := &TorrentSession{
		config:          cfg,
		infoHash:        infoHash,
		ourPeerID:       ourPeerID,
		pieceManager:    pieceManager,
		dhtNode:         dhtNode,
		peerSessions:    make(map[string]*PeerSession),
		pendingPeers:    make(map[string]bool),
		newPeersFromDHT: make(chan []*Peer, 10),
		peerMessages:    make(chan InboundPeerMessage, cfg.MaxPeers*10), // Increased buffer
		stop:            make(chan struct{}),
	}
	return ts
}

// Start initializes the session and starts its main loop.
func (ts *TorrentSession) Start() {
	// fmt.Printf("TorrentSession [%s]: Starting...\n", hex.EncodeToString(ts.infoHash))
	ts.wg.Add(1)
	go ts.mainLoop()

	if ts.dhtNode != nil {
		ts.dhtNode.RegisterTorrent(hex.EncodeToString(ts.infoHash), ts.newPeersFromDHT)
	}
	ts.discoverPeers()
}

// Stop signals the torrent session and all its peer sessions to stop.
func (ts *TorrentSession) Stop() {
	fmt.Printf("TorrentSession [%s]: Stopping...\n", hex.EncodeToString(ts.infoHash))
	close(ts.stop)
	ts.wg.Wait()

	if ts.dhtNode != nil {
		ts.dhtNode.DeregisterTorrent(hex.EncodeToString(ts.infoHash))
	}
	fmt.Printf("TorrentSession [%s]: Stopped.\n", hex.EncodeToString(ts.infoHash))
}

// discoverPeers asks the DHT for peers for this torrent's infohash.
func (ts *TorrentSession) discoverPeers() {
	if ts.dhtNode == nil {
		// fmt.Printf("TorrentSession [%s]: DHT node is nil, cannot discover peers.\n", hex.EncodeToString(ts.infoHash))
		return
	}
	// fmt.Printf("TorrentSession [%s]: Discovering peers via DHT...\n", hex.EncodeToString(ts.infoHash))
	ts.dhtNode.GetPeers(hex.EncodeToString(ts.infoHash))
}

// connectToPeer attempts to establish a connection with a peer at the given address.
// It manages pending connections and ensures not to exceed MaxPeers.
// If connection and handshake are successful, it starts the PeerSession and sends our bitfield.
func (ts *TorrentSession) connectToPeer(peerAddr string) {
	ts.connMx.Lock()
	if _, exists := ts.peerSessions[peerAddr]; exists {
		ts.connMx.Unlock()
		return
	}
	if ts.pendingPeers[peerAddr] {
		ts.connMx.Unlock()
		return
	}
	if len(ts.peerSessions) >= ts.config.MaxPeers {
		ts.connMx.Unlock()
		return
	}
	ts.pendingPeers[peerAddr] = true
	ts.connMx.Unlock()

	// Determine the total number of pieces for PeerSession's bitmap initialization.
	numPiecesTotal := ts.pieceManager.TotalPieces
	if ts.pieceManager.TotalPieces == 0 && ts.pieceManager.TotalLength > 0 {
		if ts.pieceManager.PieceLength > 0 {
			numPiecesTotal = uint32((ts.pieceManager.TotalLength + uint64(ts.pieceManager.PieceLength) - 1) / uint64(ts.pieceManager.PieceLength))
		} else if ts.pieceManager.TotalLength == 0 {
            numPiecesTotal = 0
        } else {
            fmt.Printf("TorrentSession [%s]: Invalid piece configuration (TotalLength > 0, PieceLength = 0) for peer %s.\n", hex.EncodeToString(ts.infoHash), peerAddr)
            ts.connMx.Lock()
            delete(ts.pendingPeers, peerAddr)
            ts.connMx.Unlock()
            return
        }
	}

	newPS, err := NewPeerSession(peerAddr, ts.infoHash, ts.ourPeerID, numPiecesTotal, ts.peerMessages)
	if err != nil {
		ts.connMx.Lock()
		delete(ts.pendingPeers, peerAddr)
		ts.connMx.Unlock()
		return
	}

	ts.connMx.Lock()
	delete(ts.pendingPeers, peerAddr)
	select {
	case <-ts.stop:
		ts.connMx.Unlock()
		newPS.Close()
		// fmt.Printf("TorrentSession [%s]: Stopped before finalizing connection to %s.\n", hex.EncodeToString(ts.infoHash), newPS.addr)
		return
	default:
	}
	if len(ts.peerSessions) >= ts.config.MaxPeers {
		ts.connMx.Unlock()
		newPS.Close()
		// fmt.Printf("TorrentSession [%s]: Max peers reached while connecting to %s. Connection closed.\n", hex.EncodeToString(ts.infoHash), newPS.addr)
		return
	}
	ts.peerSessions[newPS.addr] = newPS
	ts.connMx.Unlock()

	newPS.Start()
	// fmt.Printf("TorrentSession [%s]: Successfully connected to peer %s (PeerID: %s). Active peers: %d\n",
	//	hex.EncodeToString(ts.infoHash), newPS.addr, hex.EncodeToString(newPS.PeerID), len(ts.peerSessions))

	if numPiecesTotal > 0 {
		ourBitfieldBytes := ts.pieceManager.OurBitfield.Bytes()
		if len(ourBitfieldBytes) > 0 {
			if err := newPS.SendBitfield(ourBitfieldBytes); err != nil {
				// fmt.Printf("TorrentSession [%s]: Error sending initial bitfield to %s: %v. Removing peer.\n", hex.EncodeToString(ts.infoHash), newPS.addr, err)
				ts.removePeer(newPS.addr, "failed to send initial bitfield")
			}
		}
	}
}

// mainLoop is the central control loop for the torrent session.
// It handles incoming messages from peers, DHT peer discovery, and periodic tasks.
func (ts *TorrentSession) mainLoop() {
	defer ts.wg.Done()
	// fmt.Printf("TorrentSession [%s]: Main loop started.\n", hex.EncodeToString(ts.infoHash))

	manageConnectionsTicker := time.NewTicker(10 * time.Second)
	defer manageConnectionsTicker.Stop()
	manageChokingTicker := time.NewTicker(10 * time.Second)
	defer manageChokingTicker.Stop()

	for {
		select {
		case <-ts.stop:
			// fmt.Printf("TorrentSession [%s]: Main loop stopping...\n", hex.EncodeToString(ts.infoHash))
			ts.connMx.Lock()
			for addr, ps := range ts.peerSessions {
				ps.Close()
				delete(ts.peerSessions, addr)
			}
			ts.pendingPeers = make(map[string]bool)
			ts.connMx.Unlock()
			return

		case peers := <-ts.newPeersFromDHT:
			// fmt.Printf("TorrentSession [%s]: Received %d new peers from DHT.\n", hex.EncodeToString(ts.infoHash), len(peers))
			ts.connMx.RLock()
			numCurrentPeers := len(ts.peerSessions) + len(ts.pendingPeers)
			ts.connMx.RUnlock()
			for _, peer := range peers {
				if numCurrentPeers >= ts.config.MaxPeers {
					break
				}
				peerAddr := fmt.Sprintf("%s:%d", peer.IP, peer.Port)
				go ts.connectToPeer(peerAddr)
				numCurrentPeers++
			}

		case inboundMsg := <-ts.peerMessages:
			ts.handlePeerMessage(inboundMsg)

		case <-manageConnectionsTicker.C:
			ts.manageConnections()

		case <-manageChokingTicker.C:
			ts.manageChokingPeers()
		}
	}
}

// handleRequestMessage processes a Request message from a peer.
func (ts *TorrentSession) handleRequestMessage(peerAddr string, requestMsgPayload []byte) {
	ts.connMx.RLock()
	peerSession, ok := ts.peerSessions[peerAddr]
	ts.connMx.RUnlock()
	if !ok || peerSession.IsClosed() {
		return
	}

	if peerSession.amChoking { // We are choking this peer
		// fmt.Printf("TorrentSession [%s]: Ignoring REQUEST from %s because peer is choked by us.\n", hex.EncodeToString(ts.infoHash), peerAddr)
		return
	}

	index, begin, length, err := ParseRequestPayload(requestMsgPayload)
	if err != nil {
		fmt.Printf("TorrentSession [%s]: Error parsing REQUEST from %s: %v. Removing peer.\n", hex.EncodeToString(ts.infoHash), peerAddr, err)
		ts.removePeer(peerAddr, "bad REQUEST message")
		return
	}
	if length > BlockSize { // BEP3 suggests up to 128KB, but 16KB is standard.
		fmt.Printf("TorrentSession [%s]: Peer %s requested block length %d > standard %d. Removing peer.\n", hex.EncodeToString(ts.infoHash), peerAddr, length, BlockSize)
		ts.removePeer(peerAddr, "requested block too large")
		return
	}
    if length == 0 {
		fmt.Printf("TorrentSession [%s]: Peer %s requested zero length block. Removing peer.\n", hex.EncodeToString(ts.infoHash), peerAddr)
		ts.removePeer(peerAddr, "requested zero length block")
        return
    }
	if !ts.pieceManager.HavePiece(index) {
		// fmt.Printf("TorrentSession [%s]: Peer %s requested piece %d which we don't have. Ignoring.\n", hex.EncodeToString(ts.infoHash), peerAddr, index)
		return
	}

	blockData, err := ts.pieceManager.GetBlockData(index, begin, length)
	if err != nil {
		fmt.Printf("TorrentSession [%s]: Error getting block data (piece %d, offset %d, len %d) for peer %s: %v. Ignoring request.\n",
			hex.EncodeToString(ts.infoHash), index, begin, length, peerAddr, err)
		return
	}

	if err := peerSession.SendPiece(index, begin, blockData); err != nil {
		// fmt.Printf("TorrentSession [%s]: Error queueing PIECE message for peer %s: %v.\n", hex.EncodeToString(ts.infoHash), peerAddr, err)
		if peerSession.IsClosed() { // Error might be due to session closing
			ts.removePeer(peerAddr, "failed to send piece, session closed")
		}
	}
}

// handlePeerMessage processes a message received from an active PeerSession.
func (ts *TorrentSession) handlePeerMessage(inboundMsg InboundPeerMessage) {
	ts.connMx.RLock()
	peerSession, ok := ts.peerSessions[inboundMsg.PeerAddr]
	ts.connMx.RUnlock()
	if !ok {
		return
	}
	if peerSession.IsClosed() {
		ts.removePeer(inboundMsg.PeerAddr, "session reported closed before message handling")
		return
	}

	switch inboundMsg.Message.ID {
	case MsgChoke:
		peerSession.isChoking = true // Peer is choking us
	case MsgUnchoke:
		peerSession.isChoking = false // Peer is not choking us
		// ts.requestPiecesFromPeer(peerSession) // Now we can request
	case MsgInterested:
		peerSession.isInterested = true // Peer is interested in us
	case MsgNotInterested:
		peerSession.isInterested = false // Peer is no longer interested in us
	case MsgHave:
		idx, err := ParseHavePayload(inboundMsg.Message.Payload)
		if err != nil {
			fmt.Printf("TorrentSession [%s]: Error parsing HAVE from %s: %v. Removing peer.\n", hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr, err)
			ts.removePeer(inboundMsg.PeerAddr, "bad HAVE message")
			return
		}
		if idx < peerSession.numPiecesTotal {
			if peerSession.PeerHasPieces == nil {
				peerSession.PeerHasPieces = NewBitmap(int(peerSession.numPiecesTotal))
			}
			peerSession.PeerHasPieces.Set(int(idx))
			// ts.evaluateInterestInPeer(peerSession) // Re-evaluate if we should be interested
		} else {
			fmt.Printf("TorrentSession [%s]: Peer %s sent HAVE for out-of-bounds piece %d (total %d). Removing peer.\n", hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr, idx, peerSession.numPiecesTotal)
			ts.removePeer(inboundMsg.PeerAddr, "HAVE for invalid piece index")
		}
	case MsgBitfield:
		if peerSession.PeerHasPieces == nil {
			peerSession.PeerHasPieces = NewBitmap(int(peerSession.numPiecesTotal))
		}
		expectedByteLen := peerSession.PeerHasPieces.ByteLen()
		if len(inboundMsg.Message.Payload) == expectedByteLen {
			peerSession.PeerHasPieces.FromBytes(inboundMsg.Message.Payload)
			// ts.evaluateInterestInPeer(peerSession)
		} else {
			fmt.Printf("TorrentSession [%s]: Peer %s sent BITFIELD with incorrect length (got %d, expected %d). Removing peer.\n",
				hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr, len(inboundMsg.Message.Payload), expectedByteLen)
			ts.removePeer(inboundMsg.PeerAddr, "bad BITFIELD message (incorrect length)")
		}
	case MsgRequest:
		ts.handleRequestMessage(inboundMsg.PeerAddr, inboundMsg.Message.Payload)
	case MsgPiece:
		index, begin, block, err := ParsePiecePayload(inboundMsg.Message.Payload)
		if err != nil {
			fmt.Printf("TorrentSession [%s]: Error parsing PIECE from %s: %v. Removing peer.\n", hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr, err)
			ts.removePeer(inboundMsg.PeerAddr, "bad PIECE message")
			return
		}
		// fmt.Printf("TorrentSession [%s]: Received PIECE for index %d, offset %d, len %d from %s.\n", hex.EncodeToString(ts.infoHash), index, begin, len(block), inboundMsg.PeerAddr)
		readyForVerify, err := ts.pieceManager.AddBlock(index, begin, block)
		if err != nil {
			// fmt.Printf("TorrentSession [%s]: Error adding block (piece %d, offset %d) from %s: %v\n", hex.EncodeToString(ts.infoHash), index, begin, inboundMsg.PeerAddr, err)
			// TODO: Potentially penalize peer or re-request block.
			return
		}
		if readyForVerify {
			// fmt.Printf("TorrentSession [%s]: Piece %d from %s is ready for verification.\n", hex.EncodeToString(ts.infoHash), index, inboundMsg.PeerAddr)
			go func(pi uint32) {
				verified, verifyErr := ts.pieceManager.VerifyPiece(pi)
				if verifyErr != nil {
					// fmt.Printf("TorrentSession [%s]: Error verifying piece %d: %v\n", hex.EncodeToString(ts.infoHash), pi, verifyErr)
				} else if verified {
					// fmt.Printf("TorrentSession [%s]: Piece %d successfully verified!\n", hex.EncodeToString(ts.infoHash), pi)
					ts.broadcastHave(pi)
				} else {
					// fmt.Printf("TorrentSession [%s]: Piece %d verification FAILED.\n", hex.EncodeToString(ts.infoHash), pi)
				}
			}(index)
		}
	case MsgCancel:
		// fmt.Printf("TorrentSession [%s]: Peer %s sent CANCEL (placeholder).\n", hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr)
		// TODO: Implement cancel logic if we track outgoing requests to peers.
	case KeepAliveMsgID:
		// fmt.Printf("TorrentSession [%s]: Received KeepAlive from %s.\n", hex.EncodeToString(ts.infoHash), inboundMsg.PeerAddr)
	default:
		// fmt.Printf("TorrentSession [%s]: Received unhandled message ID %d from %s.\n", hex.EncodeToString(ts.infoHash), inboundMsg.Message.ID, inboundMsg.PeerAddr)
	}
}

// manageConnections is responsible for maintaining an optimal number of peer connections.
func (ts *TorrentSession) manageConnections() {
	ts.connMx.Lock()
	defer ts.connMx.Unlock()

	var peersToRemove []string
	for addr, ps := range ts.peerSessions {
		if ps.IsClosed() {
			peersToRemove = append(peersToRemove, addr)
		}
	}
	for _, addr := range peersToRemove {
		delete(ts.peerSessions, addr)
		// fmt.Printf("TorrentSession [%s]: Pruned closed peer session %s.\n", hex.EncodeToString(ts.infoHash), addr)
	}

	numActive := len(ts.peerSessions)
	numPending := len(ts.pendingPeers)
	if numActive < ts.config.TargetPeers && (numActive+numPending < ts.config.MaxPeers) {
		go ts.discoverPeers()
	}
}

// manageChokingPeers implements the torrent choking algorithm.
func (ts *TorrentSession) manageChokingPeers() {
	ts.connMx.Lock()
	defer ts.connMx.Unlock()
	if len(ts.peerSessions) == 0 {
		return
	}

	currentPeerSessions := make([]*PeerSession, 0, len(ts.peerSessions))
    for _, ps := range ts.peerSessions {
        currentPeerSessions = append(currentPeerSessions, ps)
    }

	unchokedCount := 0
	for _, ps := range currentPeerSessions {
		if ps.IsClosed() { continue }
		if !ps.amChoking {
			unchokedCount++
		}
	}

	if unchokedCount < ts.config.MaxUploadSlots {
		for _, ps := range currentPeerSessions {
			if ps.IsClosed() { continue }
			if unchokedCount >= ts.config.MaxUploadSlots {
				break
			}
			if ps.isInterested && ps.amChoking {
				if err := ps.UpdateChoke(false); err != nil {
					// fmt.Printf("TorrentSession [%s]: Error sending UNCHOKE to %s: %v. Peer will be removed by manageConnections.\n", hex.EncodeToString(ts.infoHash), ps.addr, err)
					ps.Close()
				} else {
					unchokedCount++
				}
			}
		}
	}

	for _, ps := range currentPeerSessions {
		if ps.IsClosed() { continue }
		if ps.amChoking {
			continue
		}
		shouldChoke := false
		reason := ""
		if !ps.isInterested {
			shouldChoke = true
			reason = "not interested"
		} else if unchokedCount > ts.config.MaxUploadSlots && ps.isInterested {
			shouldChoke = true
			reason = "over upload slots"
		}

		if shouldChoke {
			if err := ps.UpdateChoke(true); err != nil {
				// fmt.Printf("TorrentSession [%s]: Error sending CHOKE to %s (%s): %v. Peer will be removed by manageConnections.\n", hex.EncodeToString(ts.infoHash), ps.addr, reason, err)
				ps.Close()
			} else {
				unchokedCount--
			}
		}
	}
}

// broadcastHave informs all connected peers that we have successfully downloaded a new piece.
func (ts *TorrentSession) broadcastHave(pieceIndex uint32) {
	ts.connMx.RLock()
	sessionsSnapshot := make([]*PeerSession, 0, len(ts.peerSessions))
    for _, ps := range ts.peerSessions {
        sessionsSnapshot = append(sessionsSnapshot, ps)
    }
	ts.connMx.RUnlock()

	for _, peerSession := range sessionsSnapshot {
		if peerSession.IsClosed() {
			continue
		}
		if err := peerSession.SendHave(pieceIndex); err != nil {
			// Error implies peer might be closing. Let manageConnections handle removal.
			// fmt.Printf("TorrentSession [%s]: Error queueing HAVE for piece %d to peer %s: %v.\n", hex.EncodeToString(ts.infoHash), pieceIndex, peerSession.addr, err)
		}
	}
}

// Helper to get a peer's bitfield. Used for deciding interest or piece requests.
func (ts *TorrentSession) getPeerBitfield(peerAddr string) (*Bitmap, bool) {
    ts.connMx.RLock()
    defer ts.connMx.RUnlock()
    ps, exists := ts.peerSessions[peerAddr]
    if !exists || ps.IsClosed() || ps.PeerHasPieces == nil {
        return nil, false
    }
    return ps.PeerHasPieces, true
}

// removePeer closes the connection to a peer and removes it from the session.
// It ensures that PeerSession.Close() is called.
func (ts *TorrentSession) removePeer(peerAddr string, reason string) {
	ts.connMx.Lock()
	defer ts.connMx.Unlock()

	if ps, exists := ts.peerSessions[peerAddr]; exists {
		// fmt.Printf("TorrentSession [%s]: Removing peer %s. Reason: %s. Active peers before: %d\n", hex.EncodeToString(ts.infoHash), peerAddr, reason, len(ts.peerSessions))
		ps.Close() // Ensure peer session's own cleanup is triggered.
		delete(ts.peerSessions, peerAddr)
		// fmt.Printf("TorrentSession [%s]: Active peers after removal: %d\n", hex.EncodeToString(ts.infoHash), len(ts.peerSessions))
	}
	delete(ts.pendingPeers, peerAddr)
}
