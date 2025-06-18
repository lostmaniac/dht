package dht

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"math"
	// Assuming bencode.go is in the same package
	// "github.com/lostmaniac/dht/bencode"
)

// FileEntry represents a file in a multi-file torrent.
type FileEntry struct {
	Path   []string // List of directory names and the filename
	Length int64
	// MD5sum string `bencode:"md5sum,omitempty"` // Optional
}

// InfoDictionary holds the data from the 'info' dictionary of a .torrent file.
type InfoDictionary struct {
	// Bencoded fields
	Name        string                   `bencode:"name"`
	PieceLength int64                    `bencode:"piece length"`
	Pieces      string                   `bencode:"pieces"` // Concatenated SHA1 hashes of pieces
	Length      int64                    `bencode:"length,omitempty"` // For single-file torrents
	FilesRaw    []map[string]interface{} `bencode:"files,omitempty"`  // For multi-file torrents (raw decode)

	// Calculated fields
	RawInfoBytes []byte    // The exact bencoded bytes of the info dictionary
	InfoHash     []byte    // SHA1 hash of RawInfoBytes (20 bytes)
	PieceHashes  [][]byte  // Slice of 20-byte SHA1 hashes for each piece
	TotalLength  int64     // Total length of all files
	ParsedFiles  []FileEntry // Processed file entries for multi-file torrents
}

// MetainfoFile represents the entire structure of a .torrent file.
type MetainfoFile struct {
	Announce     string                   `bencode:"announce"`
	AnnounceList [][]string               `bencode:"announce-list,omitempty"`
	InfoRaw      map[string]interface{}   `bencode:"info"` // Raw map to correctly get bytes for hashing
	Comment      string                   `bencode:"comment,omitempty"`
	CreatedBy    string                   `bencode:"created by,omitempty"`
	CreationDate int64                    `bencode:"creation date,omitempty"`

	// Processed field
	Info InfoDictionary
}

// ParseMetainfoFile parses the .torrent file data and populates MetainfoFile.
func ParseMetainfoFile(data []byte) (*MetainfoFile, error) {
	decoded, err := Decode(data) // Using bencode.Decode from the same package
	if err != nil {
		return nil, fmt.Errorf("failed to bdecode torrent data: %w", err)
	}

	rootMap, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("torrent data is not a valid bencoded dictionary at root level")
	}

	mf := &MetainfoFile{}

	// --- Populate MetainfoFile top-level fields ---
	if announce, ok := rootMap["announce"].(string); ok {
		mf.Announce = announce
	} else {
		return nil, fmt.Errorf("announce field is missing or not a string")
	}

	if al, ok := rootMap["announce-list"].([]interface{}); ok {
		mf.AnnounceList = make([][]string, 0, len(al))
		for _, tierInterface := range al {
			if tier, ok := tierInterface.([]interface{}); ok {
				currentTier := make([]string, 0, len(tier))
				for _, trackerInterface := range tier {
					if tracker, ok := trackerInterface.(string); ok {
						currentTier = append(currentTier, tracker)
					}
				}
				if len(currentTier) > 0 {
					mf.AnnounceList = append(mf.AnnounceList, currentTier)
				}
			}
		}
	}

	if comment, ok := rootMap["comment"].(string); ok {
		mf.Comment = comment
	}
	if createdBy, ok := rootMap["created by"].(string); ok {
		mf.CreatedBy = createdBy
	}
	if creationDate, ok := rootMap["creation date"].(int64); ok {
		mf.CreationDate = creationDate
	}

	// --- Handle Info Dictionary ---
	infoMap, ok := rootMap["info"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("'info' dictionary is missing or not a map")
	}
	mf.InfoRaw = infoMap

	// --- Infohash Calculation ---
	var infoBuf bytes.Buffer
	// Encode uses key sorting, which is crucial for infohash
	if err := EncodeTo(&infoBuf, mf.InfoRaw); err != nil { // Using bencode.EncodeTo from the same package
		return nil, fmt.Errorf("failed to re-encode info dictionary: %w", err)
	}
	mf.Info.RawInfoBytes = infoBuf.Bytes()
	hash := sha1.Sum(mf.Info.RawInfoBytes)
	mf.Info.InfoHash = hash[:]

	// --- Populate InfoDictionary fields ---
	if name, ok := mf.InfoRaw["name"].(string); ok {
		mf.Info.Name = name
	} else {
		return nil, fmt.Errorf("info dictionary missing 'name' field or not a string")
	}

	if pl, ok := mf.InfoRaw["piece length"].(int64); ok {
		mf.Info.PieceLength = pl
	} else {
		return nil, fmt.Errorf("info dictionary missing 'piece length' field or not an integer")
	}

	if pieces, ok := mf.InfoRaw["pieces"].(string); ok {
		mf.Info.Pieces = pieces
	} else {
		return nil, fmt.Errorf("info dictionary missing 'pieces' field or not a string")
	}

	// --- Single-file vs Multi-file ---
	hasLength := false
	if length, ok := mf.InfoRaw["length"].(int64); ok {
		mf.Info.Length = length
		mf.Info.TotalLength = length
		hasLength = true
	}

	hasFiles := false
	if filesRaw, ok := mf.InfoRaw["files"].([]interface{}); ok {
		mf.Info.FilesRaw = make([]map[string]interface{}, len(filesRaw)) // Store raw for completeness
		mf.Info.ParsedFiles = make([]FileEntry, 0, len(filesRaw))
		var totalLength int64 = 0
		for i, fileInterface := range filesRaw {
			fileMap, ok := fileInterface.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("file entry in 'files' list is not a dictionary")
			}
			mf.Info.FilesRaw[i] = fileMap // Store raw

			fileLen, okLen := fileMap["length"].(int64)
			if !okLen {
				return nil, fmt.Errorf("file entry missing 'length' or not an integer")
			}
			pathInterface, okPath := fileMap["path"].([]interface{})
			if !okPath {
				return nil, fmt.Errorf("file entry missing 'path' or not a list")
			}

			filePath := make([]string, len(pathInterface))
			for j, p := range pathInterface {
				pathElement, ok := p.(string)
				if !ok {
					return nil, fmt.Errorf("file path element is not a string")
				}
				filePath[j] = pathElement
			}
			if len(filePath) == 0 {
				return nil, fmt.Errorf("file entry has empty path")
			}

			mf.Info.ParsedFiles = append(mf.Info.ParsedFiles, FileEntry{
				Path:   filePath,
				Length: fileLen,
			})
			totalLength += fileLen
		}
		mf.Info.TotalLength = totalLength
		hasFiles = true
	}

	// --- Validation: Files vs Length ---
	if hasLength && hasFiles {
		return nil, fmt.Errorf("info dictionary contains both 'length' and 'files' fields")
	}
	if !hasLength && !hasFiles {
		return nil, fmt.Errorf("info dictionary must contain either 'length' (single-file) or 'files' (multi-file) field")
	}
	if mf.Info.TotalLength < 0 { // Should be caught by individual file lengths being positive
	    return nil, fmt.Errorf("total length of torrent content cannot be negative")
    }
    // Allow zero length torrents
    // if mf.Info.TotalLength == 0 && mf.Info.PieceLength > 0 {
	// 	// This implies no pieces, which is valid for an empty torrent.
	// }


	// --- Piece Hashes Processing ---
	if len(mf.Info.Pieces)%20 != 0 {
		return nil, fmt.Errorf("'pieces' string length must be a multiple of 20, got %d", len(mf.Info.Pieces))
	}
	numPieceHashes := len(mf.Info.Pieces) / 20
	mf.Info.PieceHashes = make([][]byte, numPieceHashes)
	for i := 0; i < numPieceHashes; i++ {
		mf.Info.PieceHashes[i] = []byte(mf.Info.Pieces[i*20 : (i+1)*20])
	}

	// --- Validation: Piece Length and Number of Pieces ---
	if mf.Info.PieceLength <= 0 {
        // Allow piece length to be 0 only if total length is also 0 (empty torrent)
        if mf.Info.TotalLength == 0 && mf.Info.PieceLength == 0 {
            // This is an empty torrent. numExpectedPieces would be 0.
            // numPieceHashes should also be 0.
        } else {
		    return nil, fmt.Errorf("'piece length' must be positive, got %d", mf.Info.PieceLength)
        }
	}

	if mf.Info.TotalLength == 0 && mf.Info.PieceLength == 0 { // Empty torrent
		if numPieceHashes != 0 {
			return nil, fmt.Errorf("empty torrent (total length 0, piece length 0) should have 0 piece hashes, found %d", numPieceHashes)
		}
	} else if mf.Info.PieceLength > 0 { // Normal case for non-empty torrents
		numExpectedPieces := int(math.Ceil(float64(mf.Info.TotalLength) / float64(mf.Info.PieceLength)))
		if numPieceHashes != numExpectedPieces {
			return nil, fmt.Errorf("number of piece hashes (%d) does not match expected number of pieces (%d based on total length %d and piece length %d)",
				numPieceHashes, numExpectedPieces, mf.Info.TotalLength, mf.Info.PieceLength)
		}
	}
	// If mf.Info.PieceLength is 0 but TotalLength is > 0, it's an error caught by "piece length must be positive"

	return mf, nil
}

// GetPieceLength returns the length of a specific piece.
// The last piece may be shorter than Info.PieceLength.
func (info *InfoDictionary) GetPieceLength(pieceIndex uint32) (uint32, error) {
	if info.PieceLength == 0 { // Handles empty torrent case
		if info.TotalLength == 0 && pieceIndex == 0 {
			return 0, nil
		}
		return 0, fmt.Errorf("cannot get piece length for piece index %d when main piece length is 0 and total length is not 0", pieceIndex)
	}

	numPieces := uint32(len(info.PieceHashes))
	if pieceIndex >= numPieces {
		return 0, fmt.Errorf("piece index %d out of range (total pieces %d)", pieceIndex, numPieces)
	}

	if pieceIndex == numPieces-1 { // Last piece
		lastPieceLength := info.TotalLength % info.PieceLength
		if lastPieceLength == 0 { // If total length is a multiple of piece length
			return uint32(info.PieceLength), nil
		}
		return uint32(lastPieceLength), nil
	}
	return uint32(info.PieceLength), nil
}

// NumPieces returns the total number of pieces in the torrent.
func (info *InfoDictionary) NumPieces() uint32 {
	return uint32(len(info.PieceHashes))
}
