package dht

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
	// Assuming bep3_messages.go provides the PeerMessage type and message constants
	// And torrent_session.go provides InboundPeerMessage
)

const (
	// ProtocolString is the standard BitTorrent protocol identifier.
	ProtocolString = "BitTorrent protocol"
	// HandshakeTimeout is the timeout for completing the BitTorrent handshake.
	HandshakeTimeout = 10 * time.Second // Increased from previous peerHandshakeTimeout
	// ReadTimeout is the timeout for reading messages from a peer.
	ReadTimeout = 2 * time.Minute // Should be longer than keepAliveInterval
	// WriteTimeout is the timeout for writing messages to a peer.
	WriteTimeout = 10 * time.Second
	// KeepAliveInterval is how often we should send a keep-alive message if no other messages are sent.
	// This is also used by TorrentSession for its own logic.
	// PeerSession's own keep-alive sending is triggered if no writes for ~KeepAliveInterval/2.
	PeerKeepAliveInterval = 1 * time.Minute // Slightly shorter than ReadTimeout
	// ConnectTimeout is for establishing the initial TCP connection.
	ConnectTimeout = 5 * time.Second
)

// PeerSession represents a connection to a single peer, handling message exchange.
type PeerSession struct {
	addr                string
	conn                net.Conn
	infoHash            []byte   // 20-byte SHA1 hash of the info dictionary
	ourPeerID           []byte   // 20-byte peer ID for our client
	PeerID              []byte   // 20-byte peer ID for the remote peer (public for TorrentSession to read)

	// State fields
	amChoking           bool     // True if we are choking this peer
	amInterested        bool     // True if we are interested in this peer
	isChoking           bool     // True if this peer is choking us
	isInterested        bool     // True if this peer is interested in us
	PeerHasPieces       *Bitmap  // Bitmap of pieces the remote peer has (public for TorrentSession to update)
	numPiecesTotal      uint32   // Total number of pieces in the torrent

	incomingMessages    chan<- InboundPeerMessage // Channel to send parsed messages to TorrentSession
	outgoingMessageQueue chan []byte            // Internal queue for byte slices of complete messages to send

	stop                chan struct{}
	wg                  sync.WaitGroup
	lastReadTime        time.Time
	lastWriteTime       time.Time
	closeOnce           sync.Once
}

// NewPeerSession establishes a connection, performs a handshake, and returns a new PeerSession.
func NewPeerSession(addr string, infoHash []byte, ourPeerID []byte, numPiecesTotal uint32, incomingMsgChan chan<- InboundPeerMessage) (*PeerSession, error) {
	if len(infoHash) != 20 {
		return nil, fmt.Errorf("NewPeerSession: infoHash must be 20 bytes, got %d", len(infoHash))
	}
	if len(ourPeerID) != 20 {
		return nil, fmt.Errorf("NewPeerSession: ourPeerID must be 20 bytes, got %d", len(ourPeerID))
	}

	conn, err := net.DialTimeout("tcp", addr, ConnectTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to dial peer %s: %w", addr, err)
	}

	// Perform handshake
	handshakeMsg := make([]byte, 68)
	handshakeMsg[0] = 19 // Length of protocol string
	copy(handshakeMsg[1:20], ProtocolString)
	// 8 reserved bytes (handshakeMsg[20:28]) are zero by default
	copy(handshakeMsg[28:48], infoHash)
	copy(handshakeMsg[48:68], ourPeerID)

	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	_, err = conn.Write(handshakeMsg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to send handshake to %s: %w", addr, err)
	}

	respHandshake := make([]byte, 68)
	if _, err = io.ReadFull(conn, respHandshake); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to read handshake response from %s: %w", addr, err)
	}
	conn.SetDeadline(time.Time{}) // Clear deadline after successful handshake read

	// Validate handshake response
	if respHandshake[0] != 19 || string(respHandshake[1:20]) != ProtocolString {
		conn.Close()
		return nil, fmt.Errorf("invalid protocol string in handshake from %s", addr)
	}
	if !bytes.Equal(respHandshake[28:48], infoHash) {
		conn.Close()
		// Log peerID for debugging if available: hex.EncodeToString(respHandshake[48:68])
		return nil, fmt.Errorf("infoHash mismatch in handshake from %s", addr)
	}
	peerID := make([]byte, 20)
	copy(peerID, respHandshake[48:68])

	ps := &PeerSession{
		addr:                 addr,
		conn:                 conn,
		infoHash:             infoHash,
		ourPeerID:            ourPeerID,
		PeerID:               peerID,
		amChoking:            true,  // We start by choking the peer
		isChoking:            true,  // Assume peer starts by choking us
		amInterested:         false, // We are not interested initially
		isInterested:         false, // Peer is not interested in us initially
		PeerHasPieces:        NewBitmap(int(numPiecesTotal)), // Initialized, all false
		numPiecesTotal:       numPiecesTotal,
		incomingMessages:     incomingMsgChan,
		outgoingMessageQueue: make(chan []byte, 20), // Increased buffer for outgoing messages
		stop:                 make(chan struct{}),
		lastReadTime:         time.Now(),
		lastWriteTime:        time.Now(),
	}
	return ps, nil
}

// Start begins the read and write loops for the peer session.
func (ps *PeerSession) Start() {
	ps.wg.Add(2)
	go ps.readLoop()
	go ps.writeLoop()
	fmt.Printf("PeerSession [%s]: Started read and write loops. PeerID: %s\n", ps.addr, hex.EncodeToString(ps.PeerID))
}

// Close gracefully shuts down the peer session.
func (ps *PeerSession) Close() {
	ps.closeOnce.Do(func() {
		fmt.Printf("PeerSession [%s]: Closing connection. PeerID: %s\n", ps.addr, hex.EncodeToString(ps.PeerID))
		close(ps.stop)      // Signal goroutines to stop
		if ps.conn != nil {
			ps.conn.Close() // Close the network connection
		}
	})
}

// IsClosed checks if the stop channel is closed.
func (ps *PeerSession) IsClosed() bool {
	select {
	case <-ps.stop:
		return true
	default:
		return false
	}
}

func (ps *PeerSession) readLoop() {
	defer ps.wg.Done()
	defer ps.Close() // Ensure connection is closed if readLoop exits

	for {
		select {
		case <-ps.stop:
			fmt.Printf("PeerSession [%s]: Read loop stopping (stop signal).\n", ps.addr)
			return
		default:
		}

		if err := ps.conn.SetReadDeadline(time.Now().Add(ReadTimeout)); err != nil {
			fmt.Printf("PeerSession [%s]: Error setting read deadline: %v. Closing.\n", ps.addr, err)
			return
		}

		msgID, payload, err := ReadMessage(ps.conn) // Assumes ReadMessage is from bep3_messages
		if err != nil {
			if ps.IsClosed() { // If already closing, this might be an expected error
				fmt.Printf("PeerSession [%s]: Read loop: connection closed, exiting. Err: %v\n", ps.addr, err)
			} else if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				fmt.Printf("PeerSession [%s]: Read timeout. Closing. Err: %v\n", ps.addr, err)
			} else if err == io.EOF {
				fmt.Printf("PeerSession [%s]: Connection closed by peer (EOF). Closing. Err: %v\n", ps.addr, err)
			} else {
				fmt.Printf("PeerSession [%s]: Error reading message: %v. Closing.\n", ps.addr, err)
			}
			return // Exit loop, defer ps.Close() will handle actual closing
		}
		ps.lastReadTime = time.Now()

		if msgID == KeepAliveMsgID { // Assuming KeepAliveMsgID is defined in bep3_messages
			// fmt.Printf("PeerSession [%s]: Received KeepAlive.\n", ps.addr)
			continue
		}

		// Non-blocking send to TorrentSession or drop if channel is full to prevent deadlock
		// This is crucial if TorrentSession is slow.
		select {
		case ps.incomingMessages <- InboundPeerMessage{PeerAddr: ps.addr, Message: &PeerMessage{ID: msgID, Payload: payload}}:
		case <-ps.stop:
			fmt.Printf("PeerSession [%s]: Read loop stopping while trying to send to incomingMessages.\n", ps.addr)
			return
		case <-time.After(1 * time.Second): // Timeout for sending to TorrentSession
			fmt.Printf("PeerSession [%s]: Warning - Timeout sending message (ID %d) to TorrentSession. Message dropped.\n", ps.addr, msgID)
		}
	}
}

func (ps *PeerSession) writeLoop() {
	defer ps.wg.Done()
	defer ps.Close() // Ensure connection is closed if writeLoop exits

	keepAliveTicker := time.NewTicker(PeerKeepAliveInterval / 2) // Send keep-alives proactively
	defer keepAliveTicker.Stop()

	for {
		select {
		case msgBytes, ok := <-ps.outgoingMessageQueue:
			if !ok { // Channel closed, usually means we are stopping
				fmt.Printf("PeerSession [%s]: Write loop: outgoing queue closed, exiting.\n", ps.addr)
				return
			}
			if err := ps.conn.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil {
				fmt.Printf("PeerSession [%s]: Error setting write deadline: %v. Closing.\n", ps.addr, err)
				return
			}
			if _, err := ps.conn.Write(msgBytes); err != nil {
				if !ps.IsClosed() {
					fmt.Printf("PeerSession [%s]: Error writing message: %v. Closing.\n", ps.addr, err)
				}
				return // Exit loop
			}
			ps.lastWriteTime = time.Now()
			// fmt.Printf("PeerSession [%s]: Sent message (len %d, ID %x).\n", ps.addr, len(msgBytes), msgBytes[4])


		case <-keepAliveTicker.C:
			if time.Since(ps.lastWriteTime) >= PeerKeepAliveInterval {
				// fmt.Printf("PeerSession [%s]: Sending KeepAlive due to inactivity.\n", ps.addr)
				kaMsg := NewKeepAliveMsg() // from bep3_messages
				if err := ps.conn.SetWriteDeadline(time.Now().Add(WriteTimeout)); err != nil {
					fmt.Printf("PeerSession [%s]: Error setting write deadline for keep-alive: %v. Closing.\n", ps.addr, err)
					return
				}
				if _, err := ps.conn.Write(kaMsg); err != nil {
					if !ps.IsClosed() {
						fmt.Printf("PeerSession [%s]: Error sending keep-alive: %v. Closing.\n", ps.addr, err)
					}
					return // Exit loop
				}
				ps.lastWriteTime = time.Now()
			}

		case <-ps.stop:
			fmt.Printf("PeerSession [%s]: Write loop stopping (stop signal).\n", ps.addr)
			return
		}
	}
}

// QueueMessage queues a pre-formatted message to be sent to the peer.
// Non-blocking, returns error if queue is full or session is stopped.
func (ps *PeerSession) QueueMessage(rawMsgBytes []byte) error {
	select {
	case ps.outgoingMessageQueue <- rawMsgBytes:
		return nil
	case <-ps.stop:
		return fmt.Errorf("PeerSession [%s]: stopped, cannot queue message", ps.addr)
	default: // Non-blocking if queue is full
		return fmt.Errorf("PeerSession [%s]: outgoing message queue full", ps.addr)
	}
}

// UpdateInterest tells the peer if we are interested or not.
func (ps *PeerSession) UpdateInterest(interested bool) error {
	if ps.amInterested == interested {
		return nil // No change
	}
	ps.amInterested = interested
	var msg []byte
	if interested {
		msg = NewInterestedMsg()
	} else {
		msg = NewNotInterestedMsg()
	}
	return ps.QueueMessage(msg)
}

// UpdateChoke tells the peer if we are choking or unchoking them.
func (ps *PeerSession) UpdateChoke(choking bool) error {
	if ps.amChoking == choking {
		return nil // No change
	}
	ps.amChoking = choking
	var msg []byte
	if choking {
		msg = NewChokeMsg()
	} else {
		msg = NewUnchokeMsg()
	}
	return ps.QueueMessage(msg)
}

// SendHave informs the peer that we have successfully downloaded a piece.
func (ps *PeerSession) SendHave(pieceIndex uint32) error {
	msg, err := NewHaveMsg(pieceIndex)
	if err != nil {
		return fmt.Errorf("PeerSession [%s]: error creating Have message: %w", ps.addr, err)
	}
	return ps.QueueMessage(msg)
}

// SendBitfield sends our bitfield to the peer.
// bitfieldData should be the raw byte slice representing the bitfield.
func (ps *PeerSession) SendBitfield(bitfieldData []byte) error {
	msg, err := NewBitfieldMsg(bitfieldData)
	if err != nil {
		return fmt.Errorf("PeerSession [%s]: error creating Bitfield message: %w", ps.addr, err)
	}
	return ps.QueueMessage(msg)
}

// SendRequest requests a block from the peer.
func (ps *PeerSession) SendRequest(index uint32, begin uint32, length uint32) error {
	msg, err := NewRequestMsg(index, begin, length)
	if err != nil {
		return fmt.Errorf("PeerSession [%s]: error creating Request message: %w", ps.addr, err)
	}
	return ps.QueueMessage(msg)
}

// SendPiece sends a block of a piece to the peer.
func (ps *PeerSession) SendPiece(index uint32, begin uint32, blockData []byte) error {
	msg, err := NewPieceMsg(index, begin, blockData)
	if err != nil {
		return fmt.Errorf("PeerSession [%s]: error creating Piece message: %w", ps.addr, err)
	}
	return ps.QueueMessage(msg)
}

// SendCancel cancels a previously sent block request.
func (ps *PeerSession) SendCancel(index uint32, begin uint32, length uint32) error {
	msg, err := NewCancelMsg(index, begin, length)
	if err != nil {
		return fmt.Errorf("PeerSession [%s]: error creating Cancel message: %w", ps.addr, err)
	}
	return ps.QueueMessage(msg)
}

// Wait waits for the read and write loops to complete.
// Typically called by TorrentSession after it knows a peer session is done.
func (ps *PeerSession) Wait() {
    ps.wg.Wait()
}
// Helper for NewPeerSession, not exported.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
