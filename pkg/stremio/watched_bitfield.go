package stremio

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Stremio stores watched episodes as <anchor-video-id>:<anchor-index+1>:<zlib-base64-bits>.
// Bits are indexed against the *whole* ordered episode list, not the episode
// number. Keep the anchor when reading old data whose episode list grew.
const maxWatchedBits = 16384

func decodeWatchedEpisodes(encoded string, videoIDs []string) ([]bool, error) {
	if len(videoIDs) == 0 || len(videoIDs) > maxWatchedBits {
		return nil, errors.New("invalid episode list")
	}
	parts := strings.Split(encoded, ":")
	if len(parts) < 3 {
		return nil, errors.New("invalid Stremio watched-episode field")
	}
	anchor := strings.Join(parts[:len(parts)-2], ":")
	anchorLength, err := strconv.Atoi(parts[len(parts)-2])
	if err != nil || anchorLength < 1 || anchorLength > maxWatchedBits {
		return nil, errors.New("invalid Stremio watched-episode anchor length")
	}
	packed, err := base64.StdEncoding.DecodeString(parts[len(parts)-1])
	if err != nil || len(packed) > maxWatchedBits {
		return nil, errors.New("invalid Stremio watched-episode encoding")
	}
	zr, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("invalid Stremio watched-episode compression: %w", err)
	}
	bits, readErr := io.ReadAll(io.LimitReader(zr, maxWatchedBits/8+1))
	closeErr := zr.Close()
	if readErr != nil || closeErr != nil || len(bits) > maxWatchedBits/8 {
		return nil, errors.New("corrupt Stremio watched-episode bitfield")
	}
	// The official Stremio encoder emits "undefined" for a bitfield with no
	// watched videos, including an entirely empty buffer.
	if anchor == "undefined" {
		for _, b := range bits {
			if b != 0 {
				return nil, errors.New("invalid empty Stremio watched-episode anchor")
			}
		}
		return make([]bool, len(videoIDs)), nil
	}
	if anchorLength > len(bits)*8 {
		return nil, errors.New("corrupt Stremio watched-episode anchor length")
	}
	anchorIndex := -1
	for i, id := range videoIDs {
		if id == anchor {
			anchorIndex = i
			break
		}
	}
	if anchorIndex < 0 {
		return nil, errors.New("watched-episode anchor missing from current episode metadata; refusing to guess")
	}
	// Mirror stremio-core's construct_with_videos offset calculation.
	offset := anchorLength - anchorIndex - 1
	result := make([]bool, len(videoIDs))
	for i := range result {
		previousIndex := i + offset
		if previousIndex >= 0 && previousIndex < len(videoIDs) && previousIndex < len(bits)*8 {
			result[i] = bits[previousIndex/8]&(1<<uint(previousIndex%8)) != 0
		}
	}
	return result, nil
}

func encodeWatchedEpisodes(watched []bool, videoIDs []string) (string, error) {
	if len(videoIDs) == 0 || len(videoIDs) != len(watched) || len(videoIDs) > maxWatchedBits {
		return "", errors.New("invalid watched-episode list")
	}
	bits := make([]byte, (len(watched)+7)/8)
	last := -1
	for i, done := range watched {
		if done {
			bits[i/8] |= 1 << uint(i%8)
			last = i
		}
	}
	anchor, anchorLength := "undefined", 1
	if last >= 0 {
		anchor, anchorLength = videoIDs[last], last+1
	}
	var packed bytes.Buffer
	zw, err := zlib.NewWriterLevel(&packed, 6)
	if err != nil {
		return "", err
	}
	if _, err := zw.Write(bits); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%s", anchor, anchorLength, base64.StdEncoding.EncodeToString(packed.Bytes())), nil
}

func watchedBitCount(encoded string) int {
	parts := strings.Split(encoded, ":")
	if len(parts) < 3 {
		return 0
	}
	data, err := base64.StdEncoding.DecodeString(parts[len(parts)-1])
	if err != nil || len(data) > maxWatchedBits {
		return 0
	}
	zr, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return 0
	}
	defer zr.Close()
	buf, err := io.ReadAll(io.LimitReader(zr, maxWatchedBits/8+1))
	if err != nil || len(buf) > maxWatchedBits/8 {
		return 0
	}
	n := 0
	for _, b := range buf {
		for b != 0 {
			b &= b - 1
			n++
		}
	}
	return n
}
