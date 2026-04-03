package qbittorrent

import (
	"crypto/sha1" //nolint:gosec
	"encoding/hex"
	"errors"
	"strconv"
)

// ComputeInfoHash extracts the info_hash from raw .torrent file bytes.
// The info_hash is the SHA1 of the bencoded "info" dictionary value.
func ComputeInfoHash(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty torrent data")
	}
	if data[0] != 'd' {
		return "", errors.New("invalid torrent file: not a bencoded dictionary")
	}

	pos := 1                                  // skip opening 'd'
	for pos < len(data) && data[pos] != 'e' { //nolint:gosec // pos is bounds-checked
		// Read key (must be a string)
		key, nextPos, err := readBencodeString(data, pos)
		if err != nil {
			return "", err
		}
		pos = nextPos

		if key == "info" {
			// Record start of value, skip it, extract raw bytes
			valueStart := pos
			valueEnd, err := skipBencodeValue(data, pos)
			if err != nil {
				return "", err
			}
			hash := sha1.Sum(data[valueStart:valueEnd]) //nolint:gosec
			return hex.EncodeToString(hash[:]), nil
		}

		// Skip value
		pos, err = skipBencodeValue(data, pos)
		if err != nil {
			return "", err
		}
	}

	return "", errors.New("info dictionary not found in torrent data")
}

// readBencodeString reads a bencoded string at pos, returns the string and the position after it.
func readBencodeString(data []byte, pos int) (s string, nextPos int, err error) {
	colonPos := -1
	for i := pos; i < len(data); i++ {
		if data[i] == ':' {
			colonPos = i
			break
		}
	}
	if colonPos == -1 {
		return "", 0, errors.New("invalid bencode string: no colon found")
	}

	length, err := strconv.Atoi(string(data[pos:colonPos]))
	if err != nil {
		return "", 0, errors.New("invalid bencode string length")
	}
	if length < 0 {
		return "", 0, errors.New("invalid bencode string: negative length")
	}

	start := colonPos + 1
	end := start + length
	if end > len(data) {
		return "", 0, errors.New("bencode string extends beyond data")
	}

	return string(data[start:end]), end, nil
}

// skipBencodeValue skips over a bencoded value at pos, returning the position after it.
func skipBencodeValue(data []byte, pos int) (int, error) {
	if pos >= len(data) {
		return 0, errors.New("unexpected end of bencode data")
	}

	switch {
	case data[pos] == 'i': // integer: i<number>e
		for i := pos + 1; i < len(data); i++ {
			if data[i] == 'e' {
				return i + 1, nil
			}
		}
		return 0, errors.New("unterminated bencode integer")

	case data[pos] == 'l': // list: l<items>e
		pos++
		for pos < len(data) && data[pos] != 'e' {
			var err error
			pos, err = skipBencodeValue(data, pos)
			if err != nil {
				return 0, err
			}
		}
		if pos >= len(data) {
			return 0, errors.New("unterminated bencode list")
		}
		return pos + 1, nil // skip 'e'

	case data[pos] == 'd': // dict: d<key><value>...e
		pos++
		for pos < len(data) && data[pos] != 'e' {
			// skip key (string)
			_, nextPos, err := readBencodeString(data, pos)
			if err != nil {
				return 0, err
			}
			pos = nextPos
			// skip value
			pos, err = skipBencodeValue(data, pos)
			if err != nil {
				return 0, err
			}
		}
		if pos >= len(data) {
			return 0, errors.New("unterminated bencode dictionary")
		}
		return pos + 1, nil // skip 'e'

	case data[pos] >= '0' && data[pos] <= '9': // string: <length>:<data>
		_, end, err := readBencodeString(data, pos)
		if err != nil {
			return 0, err
		}
		return end, nil

	default:
		return 0, errors.New("invalid bencode value")
	}
}
