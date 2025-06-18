package dht

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"math"
	// Assuming bitmap.go exists in the same package or is appropriately imported
	// For this subtask, we'll assume it provides:
	// type Bitmap struct { ... }
	// func NewBitmap(size int) *Bitmap
	// func (b *Bitmap) Set(i int)
	// func (b *Bitmap) IsSet(i int) bool
	// func (b *Bitmap) Len() int
	// func (b *Bitmap) AllSet() bool // Helper that might be useful
)

const (
	// BlockSize is the standard size of a block requested from a peer.
	BlockSize = 16384 // 16 KiB
)

// PieceInfo holds static information about a piece.
type PieceInfo struct {
	Index  uint32
	Length uint32
	Hash   []byte // SHA1 hash of the piece data
}

// BlockInfo describes a block within a piece.
type BlockInfo struct {
	PieceIndex uint32
	Begin      uint32 // Offset within the piece
	Length     uint32
}

// PieceState manages the state of an individual piece being downloaded.
type PieceState struct {
	PieceInfo     PieceInfo
	HaveAllBlocks *Bitmap // Tracks downloaded blocks for this piece
	Data          []byte  // Buffer for the piece data
	BlocksPending map[uint32]bool // Tracks currently requested blocks: block_offset -> true (true indicates pending)
	IsComplete    bool    // True if downloaded, verified, and written (for future disk I/O)
	IsVerifying   bool    // True if currently undergoing hash verification
	numBlocks     uint32
}

// PieceManager manages all pieces for a torrent.
type PieceManager struct {
	TotalPieces uint32
	PieceLength uint32
	TotalLength uint64
	Pieces      []*PieceState
	OurBitfield *Bitmap // Bitmap of pieces we have completed and verified
	InfoHash    []byte  // Torrent infohash (stub)
	// Files       []FileEntry // For mapping pieces to files (stub)
}

// NewPieceManager initializes a new PieceManager.
// pieceHashes is a slice of SHA1 hashes, one for each piece.
func NewPieceManager(numPieces uint32, pieceLength uint32, totalTorrentLength uint64, pieceHashes [][]byte) (*PieceManager, error) {
	if uint32(len(pieceHashes)) != numPieces {
		return nil, fmt.Errorf("number of pieceHashes (%d) does not match numPieces (%d)", len(pieceHashes), numPieces)
	}

	pm := &PieceManager{
		TotalPieces: numPieces,
		PieceLength: pieceLength,
		TotalLength: totalTorrentLength,
		Pieces:      make([]*PieceState, numPieces),
		OurBitfield: NewBitmap(int(numPieces)), // Assumes NewBitmap is available
	}

	for i := uint32(0); i < numPieces; i++ {
		currentPieceLength := pieceLength
		if i == numPieces-1 { // Last piece might be shorter
			remainder := totalTorrentLength % uint64(pieceLength)
			if remainder != 0 {
				currentPieceLength = uint32(remainder)
			}
		}
		if currentPieceLength == 0 && totalTorrentLength > 0 { // Handle 0-length torrents or last piece being 0 if totalLength is multiple of pieceLength
			if totalTorrentLength > 0 && i == numPieces-1 && totalTorrentLength % uint64(pieceLength) == 0 {
				currentPieceLength = pieceLength
			} else {
				return nil, fmt.Errorf("piece %d has zero length for a non-zero total length", i)
			}
		}


		numBlocksInPiece := (currentPieceLength + BlockSize - 1) / BlockSize

		pm.Pieces[i] = &PieceState{
			PieceInfo: PieceInfo{
				Index:  i,
				Length: currentPieceLength,
				Hash:   pieceHashes[i],
			},
			HaveAllBlocks: NewBitmap(int(numBlocksInPiece)),
			Data:          make([]byte, currentPieceLength),
			BlocksPending: make(map[uint32]bool),
			IsComplete:    false,
			IsVerifying:   false,
			numBlocks:     numBlocksInPiece,
		}
	}
	return pm, nil
}

// HavePiece checks if we have a fully downloaded and verified piece.
func (pm *PieceManager) HavePiece(pieceIndex uint32) bool {
	if pieceIndex >= pm.TotalPieces {
		return false
	}
	return pm.OurBitfield.IsSet(int(pieceIndex))
}

// NeedPiece checks if we do not have a fully downloaded and verified piece.
func (pm *PieceManager) NeedPiece(pieceIndex uint32) bool {
	if pieceIndex >= pm.TotalPieces {
		return false // Or handle as an error, depending on desired strictness
	}
	return !pm.OurBitfield.IsSet(int(pieceIndex))
}

// AddBlock adds a downloaded block to the respective piece.
// Returns true if the piece now has all its blocks and is ready for verification.
func (pm *PieceManager) AddBlock(pieceIndex uint32, beginOffset uint32, blockData []byte) (pieceReadyForVerification bool, err error) {
	if pieceIndex >= pm.TotalPieces {
		return false, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	ps := pm.Pieces[pieceIndex]
	if ps.IsComplete {
		return false, fmt.Errorf("piece %d is already complete", pieceIndex)
	}
	if ps.IsVerifying {
		return false, fmt.Errorf("piece %d is currently being verified", pieceIndex)
	}

	if beginOffset%BlockSize != 0 {
		return false, fmt.Errorf("block begin offset %d is not a multiple of BlockSize %d", beginOffset, BlockSize)
	}
	blockIndexInPiece := beginOffset / BlockSize

	if blockIndexInPiece >= ps.numBlocks {
		return false, fmt.Errorf("block offset %d (index %d) is out of range for piece %d (num blocks %d)",
			beginOffset, blockIndexInPiece, pieceIndex, ps.numBlocks)
	}

	expectedBlockLength := BlockSize
	if blockIndexInPiece == ps.numBlocks-1 { // Last block of the piece
		remainder := ps.PieceInfo.Length % BlockSize
		if remainder != 0 {
			expectedBlockLength = remainder
		}
	}
    if uint32(len(blockData)) != expectedBlockLength {
         // Allow for the case where the final block of the entire torrent is smaller than BlockSize
        isLastPiece := pieceIndex == pm.TotalPieces -1
        isLastBlockInPiece := blockIndexInPiece == ps.numBlocks -1
        if isLastPiece && isLastBlockInPiece {
            actualLastBlockLength := pm.TotalLength - uint64(pieceIndex)*uint64(pm.PieceLength) - uint64(beginOffset)
            if uint64(len(blockData)) != actualLastBlockLength {
                 return false, fmt.Errorf("block data length %d for piece %d, block %d (last block of torrent) does not match expected last block length %d",
                    len(blockData), pieceIndex, blockIndexInPiece, actualLastBlockLength)
            }
        } else {
            return false, fmt.Errorf("block data length %d for piece %d, block %d does not match expected length %d",
                len(blockData), pieceIndex, blockIndexInPiece, expectedBlockLength)
        }
    }


	if ps.HaveAllBlocks.IsSet(int(blockIndexInPiece)) {
		// We already have this block. Could be a duplicate from another peer.
		// For now, we'll just ignore it but clear pending status.
		delete(ps.BlocksPending, beginOffset)
		return false, nil // Not an error, but piece isn't newly ready for verification from this block
	}

	copy(ps.Data[beginOffset:beginOffset+uint32(len(blockData))], blockData)
	ps.HaveAllBlocks.Set(int(blockIndexInPiece))
	delete(ps.BlocksPending, beginOffset)

	// Check if all blocks for this piece are now downloaded
	// Assumes Bitmap has an AllSet() method or similar logic can be implemented by checking Len against set count.
	// For now, let's iterate if AllSet() isn't available.
	allBlocksPresent := true
	for i := 0; i < int(ps.numBlocks); i++ {
		if !ps.HaveAllBlocks.IsSet(i) {
			allBlocksPresent = false
			break
		}
	}

	if allBlocksPresent && !ps.IsComplete { // Check !ps.IsComplete to avoid re-triggering verification
		return true, nil
	}

	return false, nil
}

// VerifyPiece checks the SHA1 hash of a piece's data.
// It updates the piece's and PieceManager's state if verification is successful.
func (pm *PieceManager) VerifyPiece(pieceIndex uint32) (bool, error) {
	if pieceIndex >= pm.TotalPieces {
		return false, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	ps := pm.Pieces[pieceIndex]
	if ps.IsComplete {
		return true, nil // Already verified
	}
	if ps.IsVerifying {
		return false, errors.New("piece is already being verified")
	}

	// Check if all blocks are present before verification
	allBlocksPresent := true
	for i := 0; i < int(ps.numBlocks); i++ {
		if !ps.HaveAllBlocks.IsSet(i) {
			allBlocksPresent = false
			break
		}
	}
	if !allBlocksPresent {
		return false, errors.New("not all blocks received for piece, cannot verify")
	}

	ps.IsVerifying = true
	defer func() { ps.IsVerifying = false }()

	hash := sha1.Sum(ps.Data)

	if string(hash[:]) == string(ps.PieceInfo.Hash) {
		ps.IsComplete = true
		pm.OurBitfield.Set(int(pieceIndex))
		// Optional: Free ps.Data if no longer needed and disk persistence is handled
		// ps.Data = nil
		// ps.HaveAllBlocks = nil // No longer needed after completion
		// ps.BlocksPending = nil
		return true, nil
	}

	// Verification failed: reset piece state for re-download
	ps.HaveAllBlocks = NewBitmap(int(ps.numBlocks)) // Reset bitmap
	ps.BlocksPending = make(map[uint32]bool)     // Clear pending blocks
	// ps.Data will be overwritten by new blocks.
	return false, errors.New("piece hash verification failed")
}

// SelectBlocksToRequest selects up to numBlocks blocks that are needed for a given piece
// and are not currently pending.
func (pm *PieceManager) SelectBlocksToRequest(pieceIndex uint32, numBlocksToSelect int) ([]BlockInfo, error) {
	if pieceIndex >= pm.TotalPieces {
		return nil, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	ps := pm.Pieces[pieceIndex]
	if ps.IsComplete || ps.IsVerifying {
		return nil, fmt.Errorf("piece %d is complete or being verified, no blocks to select", pieceIndex)
	}

	var selectedBlocks []BlockInfo
	count := 0

	for blockIdx := uint32(0); blockIdx < ps.numBlocks; blockIdx++ {
		if count >= numBlocksToSelect {
			break
		}

		blockOffset := blockIdx * BlockSize
		if !ps.HaveAllBlocks.IsSet(int(blockIdx)) && !ps.BlocksPending[blockOffset] {
			currentBlockLength := BlockSize
			if blockIdx == ps.numBlocks-1 { // Last block of the piece
				remainder := ps.PieceInfo.Length % BlockSize
				if remainder != 0 {
					currentBlockLength = remainder
				}
			}
            // If piece length is 0, this block should not exist.
            // However, numBlocks calculation should handle this.
            // If currentBlockLength is 0, it means the piece itself is 0 length or miscalculation.
            if currentBlockLength == 0 && ps.PieceInfo.Length > 0 {
                 // This case should ideally not happen if numBlocks is calculated correctly.
                 // It might happen if piece length is not a multiple of BlockSize and last block logic is tricky.
                 // For a piece of length 0, numBlocks should be 0.
                 continue
            }


			selectedBlocks = append(selectedBlocks, BlockInfo{
				PieceIndex: pieceIndex,
				Begin:      blockOffset,
				Length:     currentBlockLength,
			})
			ps.BlocksPending[blockOffset] = true
			count++
		}
	}

	if len(selectedBlocks) == 0 && !ps.IsComplete {
        // If no blocks were selected, and the piece isn't complete,
        // it implies all needed blocks are currently pending.
        // Check if all blocks are either had or pending
        allNeededArePendingOrHad := true
        for blockIdx := uint32(0); blockIdx < ps.numBlocks; blockIdx++ {
            if !ps.HaveAllBlocks.IsSet(int(blockIdx)) && !ps.BlocksPending[blockIdx*BlockSize] {
                allNeededArePendingOrHad = false
                break
            }
        }
        if allNeededArePendingOrHad && ps.numBlocks > 0 { // ensure not a zero-block piece
             // This is a valid state, just means we're waiting for peers
        } else if ps.numBlocks > 0 { // Only return error if there are blocks to get
            // This could indicate an issue if we expected to select blocks
            // but found none and not all are pending/had.
            // However, for this function, returning an empty slice is okay.
        }
	}
	return selectedBlocks, nil
}

// GetPieceForVerification returns the data and expected hash for a piece if all its blocks have been downloaded.
func (pm *PieceManager) GetPieceForVerification(pieceIndex uint32) (data []byte, hash []byte, err error) {
	if pieceIndex >= pm.TotalPieces {
		return nil, nil, fmt.Errorf("piece index %d out of range", pieceIndex)
	}
	ps := pm.Pieces[pieceIndex]
	if ps.IsComplete {
		return ps.Data, ps.PieceInfo.Hash, nil // Or error if data already cleared
	}

	allBlocksPresent := true
	for i := 0; i < int(ps.numBlocks); i++ {
		if !ps.HaveAllBlocks.IsSet(i) {
			allBlocksPresent = false
			break
		}
	}

	if !allBlocksPresent {
		return nil, nil, fmt.Errorf("not all blocks received for piece %d", pieceIndex)
	}
	return ps.Data, ps.PieceInfo.Hash, nil
}

// MarkPieceCompleted directly sets a piece as complete in the PieceManager's bitfield.
// This is useful for resuming downloads or if verification is handled externally.
func (pm *PieceManager) MarkPieceCompleted(pieceIndex uint32) error {
	if pieceIndex >= pm.TotalPieces {
		return fmt.Errorf("piece index %d out of range", pieceIndex)
	}
	ps := pm.Pieces[pieceIndex]
	ps.IsComplete = true // Also mark in PieceState
	pm.OurBitfield.Set(int(pieceIndex))
	return nil
}

// TotalBlocksForPiece returns the total number of blocks for a given piece.
func (pm *PieceManager) TotalBlocksForPiece(pieceIndex uint32) (uint32, error) {
    if pieceIndex >= pm.TotalPieces {
        return 0, fmt.Errorf("piece index %d out of range", pieceIndex)
    }
    return pm.Pieces[pieceIndex].numBlocks, nil
}

// GetPieceLength returns the length of a specific piece.
func (pm *PieceManager) GetPieceLength(pieceIndex uint32) (uint32, error) {
	if pieceIndex >= pm.TotalPieces {
		return 0, fmt.Errorf("piece index %d out of range", pieceIndex)
	}
	return pm.Pieces[pieceIndex].PieceInfo.Length, nil
}

// --- Bitmap Placeholder ---
// This is a placeholder for bitmap.Bitmap.
// In a real scenario, this would come from an actual bitmap package.

type Bitmap struct {
	bits []bool
	size int
}

func NewBitmap(size int) *Bitmap {
	if size < 0 { size = 0 }
	return &Bitmap{
		bits: make([]bool, size),
		size: size,
	}
}

func (b *Bitmap) Set(i int) {
	if i >= 0 && i < b.size {
		b.bits[i] = true
	}
}

func (b *Bitmap) IsSet(i int) bool {
	if i >= 0 && i < b.size {
		return b.bits[i]
	}
	return false
}

func (b *Bitmap) Len() int {
	return b.size
}

// AllSet checks if all bits in the bitmap are set.
func (b *Bitmap) AllSet() bool {
	if b.size == 0 { // An empty bitmap could be considered "all set" or not, depending on context.
		return true // Assuming for piece completion, 0 blocks means complete.
	}
	for _, bit := range b.bits {
		if !bit {
			return false
		}
	}
	return true
}

// Bytes returns the byte representation of the bitmap.
func (b *Bitmap) Bytes() []byte {
	byteLen := (b.size + 7) / 8
	bytes := make([]byte, byteLen)
	for i := 0; i < b.size; i++ {
		if b.bits[i] {
			byteIndex := i / 8
			bitIndex := 7 - (i % 8) // Big-endian bit order within a byte
			bytes[byteIndex] |= (1 << bitIndex)
		}
	}
	return bytes
}

// ByteLen returns the length of the bitmap in bytes.
func (b *Bitmap) ByteLen() int {
	return (b.size + 7) / 8
}

// FromBytes populates the bitmap from a byte slice.
// Assumes the byte slice is the correct length for the bitmap's size.
func (b *Bitmap) FromBytes(bytes []byte) {
	if len(bytes) != b.ByteLen() {
		// Or handle error appropriately
		return
	}
	for i := 0; i < b.size; i++ {
		byteIndex := i / 8
		bitIndex := 7 - (i % 8)
		if (bytes[byteIndex]>>bitIndex)&1 == 1 {
			b.bits[i] = true
		} else {
			b.bits[i] = false
		}
	}
}


// GetBlockData retrieves a block of data from a completed piece.
func (pm *PieceManager) GetBlockData(pieceIndex uint32, beginOffset uint32, length uint32) ([]byte, error) {
	if pieceIndex >= pm.TotalPieces {
		return nil, fmt.Errorf("piece index %d out of range", pieceIndex)
	}

	ps := pm.Pieces[pieceIndex]

	// Check if we have the piece (i.e., it's complete and verified)
	// Using ps.IsComplete is more direct if AddBlock/VerifyPiece maintain it correctly.
	// Alternatively, pm.OurBitfield.IsSet(int(pieceIndex)) can be used.
	if !ps.IsComplete {
		return nil, fmt.Errorf("piece %d is not complete or not verified", pieceIndex)
	}

	if ps.Data == nil {
		// This might happen if data is cleared after verification to save memory,
		// though current implementation doesn't do that by default.
		return nil, fmt.Errorf("piece %d data is not available (possibly cleared after verification)", pieceIndex)
	}

	if beginOffset >= ps.PieceInfo.Length {
		return nil, fmt.Errorf("begin offset %d is out of bounds for piece %d (length %d)",
			beginOffset, pieceIndex, ps.PieceInfo.Length)
	}

	if beginOffset+length > ps.PieceInfo.Length {
		// Adjust length if request goes beyond the piece boundary
		// This is allowed by BEP3, peer might request more than available if it's the last block.
		// However, our GetBlockData should only return what's valid.
		// The request length validation (e.g. against BlockSize) should be done by the caller (TorrentSession)
		// For safety, we truncate here to actual available data.
		length = ps.PieceInfo.Length - beginOffset
		if length == 0 { // Should not happen if beginOffset < ps.PieceInfo.Length
			return []byte{}, nil // Requesting 0 bytes from a valid offset
		}
	}

	if length == 0 { // Requesting zero bytes
		return []byte{}, nil
	}


	blockData := make([]byte, length)
	copy(blockData, ps.Data[beginOffset:beginOffset+length])

	return blockData, nil
}
