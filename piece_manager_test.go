package dht

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"reflect"
	"testing"
)

// Helper to create deterministic piece hashes for testing
func generateTestPieceHashes(numPieces int, pieceLength uint32) ([][]byte, [][]byte) {
	pieceHashes := make([][]byte, numPieces)
	pieceData := make([][]byte, numPieces) // Store the actual data to verify against later

	for i := 0; i < numPieces; i++ {
		// For simplicity, make last piece full length for hash generation,
		// actual piece length handling is tested in NewPieceManager
		data := make([]byte, pieceLength)
		rand.Read(data) // Fill with random data to make hashes unique
		pieceData[i] = data

		hash := sha1.Sum(data)
		pieceHashes[i] = hash[:]
	}
	return pieceHashes, pieceData
}

func TestNewPieceManager(t *testing.T) {
	numPieces := uint32(3)
	pieceLength := uint32(BlockSize * 2) // Each piece is 2 blocks long
	totalLength := uint64(numPieces * pieceLength)
	rawHashes, _ := generateTestPieceHashes(int(numPieces), pieceLength)

	pm, err := NewPieceManager(numPieces, pieceLength, totalLength, rawHashes)
	if err != nil {
		t.Fatalf("NewPieceManager() error = %v", err)
	}

	if pm.TotalPieces != numPieces || pm.PieceLength != pieceLength || pm.TotalLength != totalLength {
		t.Errorf("NewPieceManager() basic properties mismatch")
	}
	if len(pm.Pieces) != int(numPieces) {
		t.Errorf("NewPieceManager() len(pm.Pieces) = %d, want %d", len(pm.Pieces), numPieces)
	}
	if pm.OurBitfield.Len() != int(numPieces) {
		t.Errorf("NewPieceManager() OurBitfield.Len() = %d, want %d", pm.OurBitfield.Len(), numPieces)
	}

	for i := uint32(0); i < numPieces; i++ {
		ps := pm.Pieces[i]
		if ps.PieceInfo.Index != i {
			t.Errorf("Piece %d: Index = %d, want %d", i, ps.PieceInfo.Index, i)
		}
		if ps.PieceInfo.Length != pieceLength { // Assuming no last piece truncation for this test
			t.Errorf("Piece %d: Length = %d, want %d", i, ps.PieceInfo.Length, pieceLength)
		}
		if !bytes.Equal(ps.PieceInfo.Hash, rawHashes[i]) {
			t.Errorf("Piece %d: Hash mismatch", i)
		}
		if ps.HaveAllBlocks.Len() != 2 { // 2 blocks per piece
			t.Errorf("Piece %d: HaveAllBlocks.Len() = %d, want 2", i, ps.HaveAllBlocks.Len())
		}
		if ps.IsComplete || ps.IsVerifying {
			t.Errorf("Piece %d: Should not be complete or verifying initially", i)
		}
		if len(ps.Data) != int(pieceLength) {
			t.Errorf("Piece %d: Data buffer size = %d, want %d", i, len(ps.Data), pieceLength)
		}
	}

	// Test with last piece shorter
	totalLengthShort := uint64(pieceLength*2 + pieceLength/2) // 2.5 pieces
	rawHashesShort, _ := generateTestPieceHashes(3, pieceLength) // Hashes for 3 full pieces
	pmShort, errShort := NewPieceManager(3, pieceLength, totalLengthShort, rawHashesShort)
	if errShort != nil {
		t.Fatalf("NewPieceManager(short last piece) error = %v", errShort)
	}
	if pmShort.Pieces[2].PieceInfo.Length != pieceLength/2 {
		t.Errorf("Last piece length = %d, want %d", pmShort.Pieces[2].PieceInfo.Length, pieceLength/2)
	}
	if pmShort.Pieces[2].numBlocks != 1 { // (pieceLength/2 + BlockSize -1) / BlockSize
		t.Errorf("Last piece numBlocks = %d, want 1", pmShort.Pieces[2].numBlocks)
	}

	// Test invalid args: hash count mismatch
	_, err = NewPieceManager(numPieces, pieceLength, totalLength, rawHashes[:numPieces-1])
	if err == nil {
		t.Error("NewPieceManager expected error for hash count mismatch, got nil")
	}
}

func TestPieceManager_AddBlock(t *testing.T) {
	numPieces := uint32(1)
	pieceLength := uint32(BlockSize * 2) // 2 blocks
	totalLength := uint64(numPieces * pieceLength)
	rawHashes, pieceContents := generateTestPieceHashes(int(numPieces), pieceLength)
	originalPieceData := pieceContents[0] // Data used for hash generation

	pm, _ := NewPieceManager(numPieces, pieceLength, totalLength, rawHashes)

	block0Data := originalPieceData[0:BlockSize]
	block1Data := originalPieceData[BlockSize : BlockSize*2]

	// Add block 0
	ready, err := pm.AddBlock(0, 0, block0Data)
	if err != nil {
		t.Fatalf("AddBlock(0,0) error = %v", err)
	}
	if ready {
		t.Errorf("AddBlock(0,0) ready = true, want false")
	}
	if !pm.Pieces[0].HaveAllBlocks.IsSet(0) {
		t.Error("Block 0 not marked in HaveAllBlocks")
	}
	if _, pending := pm.Pieces[0].BlocksPending[0]; pending {
		t.Error("Block 0 still marked as pending")
	}

	// Add block 1
	ready, err = pm.AddBlock(0, BlockSize, block1Data)
	if err != nil {
		t.Fatalf("AddBlock(0,BlockSize) error = %v", err)
	}
	if !ready {
		t.Errorf("AddBlock(0,BlockSize) ready = false, want true")
	}
	if !pm.Pieces[0].HaveAllBlocks.IsSet(1) {
		t.Error("Block 1 not marked in HaveAllBlocks")
	}

	// Verify data copied correctly
	if !bytes.Equal(pm.Pieces[0].Data[:BlockSize], block0Data) {
		t.Error("Block 0 data mismatch")
	}
	if !bytes.Equal(pm.Pieces[0].Data[BlockSize:], block1Data) {
		t.Error("Block 1 data mismatch")
	}

	// Add duplicate block
	ready, err = pm.AddBlock(0, 0, block0Data)
	if err != nil {
		t.Errorf("AddBlock(duplicate) error = %v", err)
	}
	if ready {
		t.Errorf("AddBlock(duplicate) ready = true, want false")
	}

	// Error cases
	_, err = pm.AddBlock(1, 0, block0Data) // Invalid piece index
	if err == nil {
		t.Error("AddBlock(invalid piece index) expected error")
	}
	_, err = pm.AddBlock(0, BlockSize*2, block0Data) // Invalid offset
	if err == nil {
		t.Error("AddBlock(invalid offset) expected error")
	}
	_, err = pm.AddBlock(0, 0, make([]byte, BlockSize-1)) // Invalid block length
	if err == nil {
		t.Error("AddBlock(invalid length) expected error")
	}
	pm.Pieces[0].IsComplete = true
	_, err = pm.AddBlock(0, 0, block0Data) // Piece already complete
	if err == nil {
		t.Error("AddBlock(piece complete) expected error")
	}
	pm.Pieces[0].IsComplete = false
}

func TestPieceManager_VerifyPiece(t *testing.T) {
	numPieces := uint32(1)
	pieceLength := uint32(BlockSize)
	totalLength := uint64(numPieces * pieceLength)
	rawHashes, pieceContents := generateTestPieceHashes(int(numPieces), pieceLength)
	originalPieceData := pieceContents[0]

	pm, _ := NewPieceManager(numPieces, pieceLength, totalLength, rawHashes)

	// Case 1: Not all blocks received
	verified, err := pm.VerifyPiece(0)
	if err == nil || verified {
		t.Errorf("VerifyPiece(incomplete) verified = %v, err = %v; want false, error", verified, err)
	}

	// Add the block
	pm.AddBlock(0, 0, originalPieceData)

	// Case 2: Successful verification
	verified, err = pm.VerifyPiece(0)
	if err != nil || !verified {
		t.Errorf("VerifyPiece(success) verified = %v, err = %v; want true, nil", verified, err)
	}
	if !pm.Pieces[0].IsComplete {
		t.Error("Piece not marked IsComplete after successful verification")
	}
	if !pm.OurBitfield.IsSet(0) {
		t.Error("OurBitfield not set after successful verification")
	}

	// Case 3: Attempt to re-verify already complete piece
	verified, err = pm.VerifyPiece(0)
	if err != nil || !verified {
		t.Errorf("VerifyPiece(already complete) verified = %v, err = %v; want true, nil", verified, err)
	}

	// Case 4: Failed verification (hash mismatch)
	pm.Pieces[0].IsComplete = false // Reset for test
	pm.OurBitfield = NewBitmap(1)   // Reset bitfield
	pm.AddBlock(0, 0, make([]byte, pieceLength)) // Add different data

	// Need to re-set HaveAllBlocks because AddBlock for a duplicate (even if different content) might not re-trigger ready.
	// Or, ensure AddBlock overwrites if called again for a block. The current AddBlock ignores if HaveAllBlocks.IsSet.
	// For this test, let's manually reset the piece state for a clean failure test.
	pm.Pieces[0].HaveAllBlocks = NewBitmap(int(pm.Pieces[0].numBlocks))
	pm.AddBlock(0,0, make([]byte, pieceLength)) // all zeros, will not match random hash

	verified, err = pm.VerifyPiece(0)
	if err == nil || verified {
		t.Errorf("VerifyPiece(failure) verified = %v, err = %v; want false, error", verified, err)
	}
	if pm.Pieces[0].IsComplete {
		t.Error("Piece marked IsComplete after failed verification")
	}
	if pm.OurBitfield.IsSet(0) { // Should not be set
		t.Error("OurBitfield set after failed verification")
	}
	// Check if HaveAllBlocks was reset (all bits should be false)
	if pm.Pieces[0].HaveAllBlocks.IsSet(0) {
		t.Error("HaveAllBlocks not reset after failed verification")
	}
}

func TestPieceManager_SelectBlocksToRequest(t *testing.T) {
	numPieces := uint32(1)
	pieceLength := uint32(BlockSize * 3) // 3 blocks
	totalLength := uint64(numPieces * pieceLength)
	rawHashes, _ := generateTestPieceHashes(int(numPieces), pieceLength)
	pm, _ := NewPieceManager(numPieces, pieceLength, totalLength, rawHashes)

	// Select 2 blocks
	blocks, err := pm.SelectBlocksToRequest(0, 2)
	if err != nil {
		t.Fatalf("SelectBlocksToRequest(1) error = %v", err)
	}
	if len(blocks) != 2 {
		t.Errorf("SelectBlocksToRequest(1) len = %d, want 2", len(blocks))
	}
	if blocks[0].Begin != 0 || blocks[1].Begin != BlockSize {
		t.Errorf("SelectBlocksToRequest(1) incorrect block offsets: %d, %d", blocks[0].Begin, blocks[1].Begin)
	}
	if !pm.Pieces[0].BlocksPending[0] || !pm.Pieces[0].BlocksPending[BlockSize] {
		t.Error("Selected blocks not marked as pending")
	}

	// Select remaining block
	blocks, err = pm.SelectBlocksToRequest(0, 2) // Ask for 2, but only 1 left and not pending
	if err != nil {
		t.Fatalf("SelectBlocksToRequest(2) error = %v", err)
	}
	if len(blocks) != 1 {
		t.Errorf("SelectBlocksToRequest(2) len = %d, want 1", len(blocks))
	}
	if blocks[0].Begin != BlockSize*2 {
		t.Errorf("SelectBlocksToRequest(2) incorrect block offset: %d", blocks[0].Begin)
	}
	if !pm.Pieces[0].BlocksPending[BlockSize*2] {
		t.Error("Third block not marked as pending")
	}

	// All blocks pending, select again
	blocks, err = pm.SelectBlocksToRequest(0, 1)
	if err != nil {
		t.Fatalf("SelectBlocksToRequest(3) error = %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("SelectBlocksToRequest(3) len = %d, want 0 (all pending)", len(blocks))
	}

	// Add a block, then select again
	dummyData := make([]byte, BlockSize)
	pm.AddBlock(0, 0, dummyData) // Block 0 is now had
	pm.Pieces[0].BlocksPending[0] = false // AddBlock clears this, but be explicit

	blocks, err = pm.SelectBlocksToRequest(0, 3) // Should select block 1 (pending) and block 2 (pending)
	if err != nil {
		t.Fatalf("SelectBlocksToRequest(4) error = %v", err)
	}
	// Actually, AddBlock clears pending. So only BlockSize and BlockSize*2 are pending.
	// And SelectBlocksToRequest should not select already pending blocks.
	// The previous test already set BlockSize and BlockSize*2 as pending.
	// So this should return 0.
	if len(blocks) != 0 {
		 t.Errorf("SelectBlocksToRequest(4) len = %d, want 0 (remaining are pending)", len(blocks))
	}

	// Clear a pending status and re-request
	pm.Pieces[0].BlocksPending[BlockSize] = false
	blocks, err = pm.SelectBlocksToRequest(0,1)
	if err != nil || len(blocks) != 1 || blocks[0].Begin != BlockSize {
		t.Fatalf("Failed to re-select a block after clearing pending status: len=%d, err=%v", len(blocks), err)
	}


	// Piece complete
	pm.Pieces[0].IsComplete = true
	_, err = pm.SelectBlocksToRequest(0, 1)
	if err == nil {
		t.Error("SelectBlocksToRequest(complete piece) expected error")
	}
}

func TestPieceManager_GetBlockData(t *testing.T) {
	numPieces := uint32(1)
	pieceLength := uint32(BlockSize)
	totalLength := uint64(pieceLength)
	rawHashes, pieceContents := generateTestPieceHashes(1, pieceLength)
	originalPieceData := pieceContents[0]

	pm, _ := NewPieceManager(numPieces, pieceLength, totalLength, rawHashes)

	// Case 1: Piece not complete
	_, err := pm.GetBlockData(0, 0, BlockSize)
	if err == nil {
		t.Error("GetBlockData(not complete) expected error")
	}

	// Make piece complete
	pm.AddBlock(0, 0, originalPieceData)
	pm.VerifyPiece(0) // Sets IsComplete to true

	// Case 2: Successful retrieval
	block, err := pm.GetBlockData(0, 0, BlockSize)
	if err != nil {
		t.Fatalf("GetBlockData(success) error = %v", err)
	}
	if !bytes.Equal(block, originalPieceData) {
		t.Error("GetBlockData(success) data mismatch")
	}

	// Case 3: Partial retrieval
	block, err = pm.GetBlockData(0, BlockSize/2, BlockSize/2)
	if err != nil {
		t.Fatalf("GetBlockData(partial) error = %v", err)
	}
	if !bytes.Equal(block, originalPieceData[BlockSize/2:]) {
		t.Error("GetBlockData(partial) data mismatch")
	}

	// Case 4: Invalid offset
	_, err = pm.GetBlockData(0, BlockSize, BlockSize) // Offset exactly at end of piece
	if err == nil {
		t.Error("GetBlockData(invalid offset) expected error")
	}

	// Case 5: Offset valid, but length goes out of bounds (should be truncated by GetBlockData)
	block, err = pm.GetBlockData(0, BlockSize/2, BlockSize) // Requesting more than available
	if err != nil {
		t.Fatalf("GetBlockData(len out of bounds) error = %v", err)
	}
	if len(block) != int(BlockSize/2) {
		t.Errorf("GetBlockData(len out of bounds) len = %d, want %d", len(block), BlockSize/2)
	}
	if !bytes.Equal(block, originalPieceData[BlockSize/2:]) {
		t.Error("GetBlockData(len out of bounds) data mismatch")
	}

	// Case 6: Requesting zero length
	block, err = pm.GetBlockData(0,0,0)
	if err != nil {
		t.Fatalf("GetBlockData(zero length) error = %v", err)
	}
	if len(block) != 0 {
		t.Errorf("GetBlockData(zero length) len = %d, want 0", len(block))
	}
}

// Basic Bitmap tests (as it's a placeholder)
func TestBitmap_Placeholder(t *testing.T) {
	bm := NewBitmap(10)
	if bm.Len() != 10 {
		t.Errorf("Bitmap.Len() = %d, want 10", bm.Len())
	}
	if bm.IsSet(0) {
		t.Error("Bitmap.IsSet(0) should be false initially")
	}
	if bm.AllSet() {
		t.Error("Bitmap.AllSet() should be false initially for non-empty bitmap")
	}

	bm.Set(0)
	if !bm.IsSet(0) {
		t.Error("Bitmap.IsSet(0) should be true after Set(0)")
	}
	bm.Set(9)
	if !bm.IsSet(9) {
		t.Error("Bitmap.IsSet(9) should be true after Set(9)")
	}
	// Out of bounds
	bm.Set(10)
	if bm.IsSet(10) {
		t.Error("Bitmap.IsSet(10) should be false (out of bounds)")
	}

	for i := 0; i < 10; i++ {
		bm.Set(i)
	}
	if !bm.AllSet() {
		t.Error("Bitmap.AllSet() should be true after setting all bits")
	}

	bmEmpty := NewBitmap(0)
	if !bmEmpty.AllSet() {
		t.Error("Empty Bitmap.AllSet() should be true")
	}
}
