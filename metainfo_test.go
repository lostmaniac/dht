package dht

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"reflect"
	"testing"
	"strings"
)

// Helper to create bencoded data for testing.
// This is a simplified manual bencoder for specific structures needed in tests.
func manualBencode(data interface{}) []byte {
	var buf bytes.Buffer
	err := EncodeTo(&buf, data) // Use the actual bencode.EncodeTo from the package
	if err != nil {
		panic(err) // Should not happen in tests with controlled data
	}
	return buf.Bytes()
}

// Sample torrent data
var singleFileTorrentRaw = map[string]interface{}{
	"announce": "http://tracker.example.com/announce",
	"info": map[string]interface{}{
		"name":         "singlefile.txt",
		"piece length": int64(256 * 1024), // 256 KiB
		"length":       int64(512 * 1024), // 512 KiB -> 2 pieces
		"pieces":       strings.Repeat("abcdefghijklmnopqrst", 2), // 2 * 20 bytes
	},
	"comment": "A single file torrent",
}
var singleFileTorrentBytes = manualBencode(singleFileTorrentRaw)

var multiFileTorrentRaw = map[string]interface{}{
	"announce": "http://tracker.another.org/announce",
	"announce-list": [][]string{
		{"http://tracker1.example.com/announce"},
		{"http://tracker2.example.com/announce", "http://tracker3.example.com/announce"},
	},
	"info": map[string]interface{}{
		"name":         "multifile_test_dir",
		"piece length": int64(64 * 1024), // 64 KiB
		"files": []map[string]interface{}{
			{"length": int64(100000), "path": []string{"dir1", "file1.txt"}}, // > 1 piece
			{"length": int64(30000), "path": []string{"file2.bin"}},          // < 1 piece
		},
		"pieces": strings.Repeat("12345678901234567890", 3), // (100000+30000) / 65536 = 1.98 -> 2 pieces? No, ceil(130000/65536) = 2 pieces needed for data. Hash count should match.
		                                                    // Piece 0: first 65536 of file1
		                                                    // Piece 1: remaining 34464 of file1 + first 30000 of file2
		                                                    // Piece 2: (placeholder, let's assume 3 pieces based on hash string len)
	},
	"created by": "test-suite",
}

// Correct piece calculation for multiFileTorrentRaw:
// File1: 100000 bytes. PieceLength: 65536.
//   - Piece 0: bytes 0-65535 of file1.
//   - Piece 1: bytes 65536-99999 of file1 (34464 bytes)
// File2: 30000 bytes.
//   - Piece 1 (cont.): ... takes first 30000 bytes of file2 (total in piece 1: 34464 + 30000 = 64464 bytes)
// TotalLength = 130000. NumExpectedPieces = ceil(130000 / 65536) = 2.
// So, "pieces" string should be 40 bytes long.
var multiFileTorrentRawCorrected = map[string]interface{}{
	"announce": "http://tracker.another.org/announce",
	"announce-list": [][]interface{}{ // Bencode lib expects []interface{} for lists
		[]interface{}{"http://tracker1.example.com/announce"},
		[]interface{}{"http://tracker2.example.com/announce", "http://tracker3.example.com/announce"},
	},
	"info": map[string]interface{}{
		"name":         "multifile_test_dir",
		"piece length": int64(64 * 1024),
		"files": []interface{}{ // Bencode lib expects []interface{} for lists
			map[string]interface{}{"length": int64(100000), "path": []interface{}{"dir1", "file1.txt"}},
			map[string]interface{}{"length": int64(30000), "path": []interface{}{"file2.bin"}},
		},
		"pieces": strings.Repeat("12345678901234567890", 2), // 2 * 20 bytes for 2 pieces
	},
	"created by": "test-suite",
}
var multiFileTorrentBytes = manualBencode(multiFileTorrentRawCorrected)


func TestParseMetainfoFile_SingleFile(t *testing.T) {
	mf, err := ParseMetainfoFile(singleFileTorrentBytes)
	if err != nil {
		t.Fatalf("ParseMetainfoFile(single) error = %v", err)
	}

	if mf.Announce != singleFileTorrentRaw["announce"] {
		t.Errorf("Announce mismatch: got %s, want %s", mf.Announce, singleFileTorrentRaw["announce"])
	}
	if mf.Comment != singleFileTorrentRaw["comment"] {
		t.Errorf("Comment mismatch: got %s, want %s", mf.Comment, singleFileTorrentRaw["comment"])
	}

	infoRaw := singleFileTorrentRaw["info"].(map[string]interface{})
	if mf.Info.Name != infoRaw["name"] {
		t.Errorf("Info.Name mismatch: got %s, want %s", mf.Info.Name, infoRaw["name"])
	}
	if mf.Info.PieceLength != infoRaw["piece length"] {
		t.Errorf("Info.PieceLength mismatch: got %d, want %d", mf.Info.PieceLength, infoRaw["piece length"])
	}
	if mf.Info.Length != infoRaw["length"] {
		t.Errorf("Info.Length mismatch: got %d, want %d", mf.Info.Length, infoRaw["length"])
	}
	if mf.Info.TotalLength != infoRaw["length"] { // For single file, TotalLength is Length
		t.Errorf("Info.TotalLength mismatch: got %d, want %d", mf.Info.TotalLength, infoRaw["length"])
	}
	if mf.Info.Pieces != infoRaw["pieces"] {
		t.Errorf("Info.Pieces string mismatch")
	}
	if len(mf.Info.PieceHashes) != 2 {
		t.Errorf("Expected 2 piece hashes, got %d", len(mf.Info.PieceHashes))
	}
	for i, ph := range mf.Info.PieceHashes {
		expectedPh := []byte(infoRaw["pieces"].(string)[i*20 : (i+1)*20])
		if !bytes.Equal(ph, expectedPh) {
			t.Errorf("PieceHash %d mismatch", i)
		}
	}

	// Verify InfoHash
	expectedInfoBytes, _ := Encode(infoRaw) // Use actual bencode for expected bytes
	expectedHash := sha1.Sum(expectedInfoBytes)
	if !bytes.Equal(mf.Info.InfoHash, expectedHash[:]) {
		t.Errorf("InfoHash mismatch: got %x, want %x", mf.Info.InfoHash, expectedHash)
	}
	if !bytes.Equal(mf.Info.RawInfoBytes, expectedInfoBytes) {
		t.Errorf("RawInfoBytes mismatch")
	}
}

func TestParseMetainfoFile_MultiFile(t *testing.T) {
	mf, err := ParseMetainfoFile(multiFileTorrentBytes)
	if err != nil {
		t.Fatalf("ParseMetainfoFile(multi) error = %v", err)
	}

	if mf.Announce != multiFileTorrentRawCorrected["announce"] {
		t.Errorf("Announce mismatch")
	}
	if mf.CreatedBy != multiFileTorrentRawCorrected["created by"] {
		t.Errorf("CreatedBy mismatch")
	}
	expectedAnnounceList := [][]string{
		{"http://tracker1.example.com/announce"},
		{"http://tracker2.example.com/announce", "http://tracker3.example.com/announce"},
	}
	if !reflect.DeepEqual(mf.AnnounceList, expectedAnnounceList) {
		t.Errorf("AnnounceList mismatch: got %v, want %v", mf.AnnounceList, expectedAnnounceList)
	}


	infoRaw := multiFileTorrentRawCorrected["info"].(map[string]interface{})
	if mf.Info.Name != infoRaw["name"] {
		t.Errorf("Info.Name mismatch")
	}
	if mf.Info.PieceLength != infoRaw["piece length"] {
		t.Errorf("Info.PieceLength mismatch")
	}

	expectedTotalLength := int64(100000 + 30000)
	if mf.Info.TotalLength != expectedTotalLength {
		t.Errorf("Info.TotalLength mismatch: got %d, want %d", mf.Info.TotalLength, expectedTotalLength)
	}

	if len(mf.Info.ParsedFiles) != 2 {
		t.Fatalf("Expected 2 parsed files, got %d", len(mf.Info.ParsedFiles))
	}
	file1 := mf.Info.ParsedFiles[0]
	if file1.Length != 100000 || !reflect.DeepEqual(file1.Path, []string{"dir1", "file1.txt"}) {
		t.Errorf("ParsedFile 0 mismatch: got %+v", file1)
	}
	file2 := mf.Info.ParsedFiles[1]
	if file2.Length != 30000 || !reflect.DeepEqual(file2.Path, []string{"file2.bin"}) {
		t.Errorf("ParsedFile 1 mismatch: got %+v", file2)
	}

	if len(mf.Info.PieceHashes) != 2 { // Based on corrected total length and piece length
		t.Errorf("Expected 2 piece hashes, got %d", len(mf.Info.PieceHashes))
	}

	// Verify InfoHash
	expectedInfoBytes, _ := Encode(infoRaw)
	expectedHash := sha1.Sum(expectedInfoBytes)
	if !bytes.Equal(mf.Info.InfoHash, expectedHash[:]) {
		t.Errorf("InfoHash mismatch: got %x, want %x", mf.Info.InfoHash, expectedHash)
	}
}

func TestParseMetainfoFile_ValidationErrors(t *testing.T) {
	testCases := []struct {
		name        string
		rawTorrent  map[string]interface{}
		expectedErr string
	}{
		{
			"missing_announce",
			map[string]interface{}{"info": singleFileTorrentRaw["info"]},
			"announce field is missing",
		},
		{
			"missing_info",
			map[string]interface{}{"announce": "http://tracker.example.com"},
			"'info' dictionary is missing",
		},
		{
			"info_missing_name",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{"piece length": int64(1), "pieces": "..."}},
			"info dictionary missing 'name'",
		},
		{
			"info_missing_piece_length",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{"name": "n", "pieces": "..."}},
			"info dictionary missing 'piece length'",
		},
		{
			"info_missing_pieces",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{"name": "n", "piece length": int64(1)}},
			"info dictionary missing 'pieces'",
		},
		{
			"length_and_files_both_present",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "pieces": "12345678901234567890",
				"length": int64(10), "files": []interface{}{map[string]interface{}{"length": int64(10), "path": []interface{}{"f"}}},
			}},
			"contains both 'length' and 'files'",
		},
		{
			"neither_length_nor_files",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "pieces": "12345678901234567890",
			}},
			"must contain either 'length' (single-file) or 'files' (multi-file)",
		},
		{
			"pieces_len_not_multiple_of_20",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "length": int64(10), "pieces": "12345",
			}},
			"'pieces' string length must be a multiple of 20",
		},
		{
			"piece_length_zero_non_empty_torrent",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(0), "length": int64(10), "pieces": "12345678901234567890",
			}},
			"'piece length' must be positive",
		},
		{
			"hash_count_mismatch", // TotalLength=10, PieceLength=1 -> 10 pieces. Hashes for 1 piece.
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "length": int64(10), "pieces": "12345678901234567890",
			}},
			"number of piece hashes (1) does not match expected number of pieces (10)",
		},
		{
			"multifile_file_missing_length",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "pieces": "12345678901234567890",
				"files": []interface{}{map[string]interface{}{"path": []interface{}{"f"}}},
			}},
			"file entry missing 'length'",
		},
		{
			"multifile_file_missing_path",
			map[string]interface{}{"announce": "...", "info": map[string]interface{}{
				"name": "n", "piece length": int64(1), "pieces": "12345678901234567890",
				"files": []interface{}{map[string]interface{}{"length": int64(10)}},
			}},
			"file entry missing 'path'",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseMetainfoFile(manualBencode(tc.rawTorrent))
			if err == nil {
				t.Fatalf("Expected error containing '%s', got nil", tc.expectedErr)
			}
			if !strings.Contains(err.Error(), tc.expectedErr) {
				t.Errorf("Error message '%s' does not contain expected string '%s'", err.Error(), tc.expectedErr)
			}
		})
	}
}

func TestMetainfo_GetPieceLength_NumPieces(t *testing.T) {
	mf, _ := ParseMetainfoFile(singleFileTorrentBytes) // PieceLength 262144, TotalLength 524288

	if mf.Info.NumPieces() != 2 {
		t.Errorf("NumPieces() = %d, want 2", mf.Info.NumPieces())
	}

	len0, err0 := mf.Info.GetPieceLength(0)
	if err0 != nil || len0 != 262144 {
		t.Errorf("GetPieceLength(0) = %d, err %v; want 262144, nil", len0, err0)
	}
	len1, err1 := mf.Info.GetPieceLength(1)
	if err1 != nil || len1 != 262144 {
		t.Errorf("GetPieceLength(1) = %d, err %v; want 262144, nil", len1, err1)
	}
	_, err2 := mf.Info.GetPieceLength(2)
	if err2 == nil {
		t.Error("GetPieceLength(out of bounds) expected error, got nil")
	}

	// Test with last piece shorter
	multiRaw := multiFileTorrentRawCorrected["info"].(map[string]interface{})
	multiRaw["piece length"] = int64(100000) // PieceLength
	multiRaw["pieces"] = strings.Repeat("x", 2*20) // TotalLength 130000. NumPieces = ceil(130000/100000) = 2

	mfMultiRaw := map[string]interface{}{
		"announce": multiFileTorrentRawCorrected["announce"],
		"info": multiRaw,
	}
	mfMulti, _ := ParseMetainfoFile(manualBencode(mfMultiRaw))

	if mfMulti.Info.NumPieces() != 2 {
		t.Errorf("NumPieces() for multi = %d, want 2", mfMulti.Info.NumPieces())
	}
	lenMulti0, _ := mfMulti.Info.GetPieceLength(0)
	if lenMulti0 != 100000 {
		t.Errorf("GetPieceLength(multi, 0) = %d, want 100000", lenMulti0)
	}
	lenMulti1, _ := mfMulti.Info.GetPieceLength(1) // Last piece: 130000 % 100000 = 30000
	if lenMulti1 != 30000 {
		t.Errorf("GetPieceLength(multi, 1) = %d, want 30000", lenMulti1)
	}
}

// Test for zero-length torrent
var zeroLengthTorrentRaw = map[string]interface{}{
    "announce": "http://tracker.example.com/announce",
    "info": map[string]interface{}{
        "name":         "zerofile.txt",
        "piece length": int64(256 * 1024), // Piece length can be non-zero
        "length":       int64(0),          // Total length is zero
        "pieces":       "",                // No pieces
    },
}
var zeroLengthTorrentBytes = manualBencode(zeroLengthTorrentRaw)

func TestParseMetainfoFile_ZeroLengthTorrent(t *testing.T) {
    mf, err := ParseMetainfoFile(zeroLengthTorrentBytes)
    if err != nil {
        t.Fatalf("ParseMetainfoFile(zero_length) error = %v", err)
    }

    if mf.Info.TotalLength != 0 {
        t.Errorf("Zero length torrent TotalLength = %d, want 0", mf.Info.TotalLength)
    }
    if mf.Info.NumPieces() != 0 {
        t.Errorf("Zero length torrent NumPieces = %d, want 0", mf.Info.NumPieces())
    }
    if len(mf.Info.PieceHashes) != 0 {
        t.Errorf("Zero length torrent PieceHashes count = %d, want 0", len(mf.Info.PieceHashes))
    }

	// Infohash should still be valid
	expectedInfoBytes, _ := Encode(zeroLengthTorrentRaw["info"].(map[string]interface{}))
	expectedHash := sha1.Sum(expectedInfoBytes)
	if !bytes.Equal(mf.Info.InfoHash, expectedHash[:]) {
		t.Errorf("InfoHash mismatch for zero length: got %x, want %x", mf.Info.InfoHash, expectedHash)
	}

	// Test GetPieceLength for zero length torrent
	_, err = mf.Info.GetPieceLength(0)
	if mf.Info.PieceLength > 0 && err == nil { // If piece length was > 0, this should error for index 0 as numPieces is 0
		t.Error("GetPieceLength(0) for zero length torrent with non-zero piece length should error")
	}

	// Test case: piece length also zero for zero total length
	zeroPieceLengthInfo := map[string]interface{}{
		"name": "zerofile.txt", "piece length": int64(0), "length": int64(0), "pieces": "",
	}
	zeroPieceLengthTorrentBytes := manualBencode(map[string]interface{}{
		"announce": "http://tracker.example.com/announce", "info": zeroPieceLengthInfo,
	})
	mfZeroPL, errZeroPL := ParseMetainfoFile(zeroPieceLengthTorrentBytes)
	if errZeroPL != nil {
		t.Fatalf("ParseMetainfoFile(zero_length, zero_piece_length) error = %v", errZeroPL)
	}
    if mfZeroPL.Info.NumPieces() != 0 {
        t.Errorf("Zero length torrent with zero piece length NumPieces = %d, want 0", mfZeroPL.Info.NumPieces())
    }
	lenPiece0, errPiece0 := mfZeroPL.Info.GetPieceLength(0)
	if errPiece0 != nil || lenPiece0 != 0 { // GetPieceLength for index 0 on a 0-piece torrent
		t.Errorf("GetPieceLength(0) for zero length, zero piece length torrent: len=%d, err=%v, want 0, nil", lenPiece0, errPiece0)
	}
}
