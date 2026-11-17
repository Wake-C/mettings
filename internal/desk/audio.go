package desk

import (
	"encoding/binary"
	"errors"
	"os"
)

// PCM16 reads a sealed mono 16 kHz PCM WAV. The helper emits this exact format.
func PCM16(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 44 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("invalid clip WAV")
	}
	valid := false
	for pos := 12; pos+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		start := pos + 8
		if size < 0 || start+size > len(b) {
			return nil, errors.New("truncated clip WAV")
		}
		switch string(b[pos : pos+4]) {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("invalid WAV format")
			}
			valid = binary.LittleEndian.Uint16(b[start:]) == 1 && binary.LittleEndian.Uint16(b[start+2:]) == 1 && binary.LittleEndian.Uint32(b[start+4:]) == 16000 && binary.LittleEndian.Uint16(b[start+14:]) == 16
		case "data":
			if !valid || size%2 != 0 {
				return nil, errors.New("expected mono 16 kHz PCM16 clip")
			}
			return b[start : start+size], nil
		}
		pos = start + size + (size % 2)
	}
	return nil, errors.New("clip contains no audio data")
}
func header(size uint32) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], size+36)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 16000)
	binary.LittleEndian.PutUint32(b[28:], 32000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], size)
	return b
}

// Append creates or extends the playable retained-audio file after a clip seals.
// Caller coordinates this short write with snapshots for transcription.
func Append(path string, pcm []byte) (offsetMS, durationMS int64, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, err
	}
	old := info.Size() - 44
	if info.Size() == 0 {
		old = 0
	} else if old < 0 {
		return 0, 0, errors.New("incomplete retained audio")
	}
	size := old + int64(len(pcm))
	if len(pcm)%2 != 0 || size > int64(^uint32(0))-36 {
		return 0, 0, errors.New("retained audio exceeds WAV size limit")
	}
	if _, err = f.WriteAt(pcm, 44+old); err != nil {
		return 0, 0, err
	}
	if _, err = f.WriteAt(header(uint32(size)), 0); err != nil {
		return 0, 0, err
	}
	if err = f.Sync(); err != nil {
		return 0, 0, err
	}
	return old / 32, int64(len(pcm)) / 32, nil
}
