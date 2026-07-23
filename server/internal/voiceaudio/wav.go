package voiceaudio

import (
	"encoding/binary"
	"errors"
)

// DecodePCM16MonoWAV validates and extracts one PCM data chunk. The caller
// supplies the only accepted sample rate and payload limit so provider
// contracts stay explicit at the boundary.
func DecodePCM16MonoWAV(wav []byte, expectedSampleRate, maxPCMBytes int) ([]byte, error) {
	if len(wav) < 12 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, errors.New("recorded voice is not a RIFF/WAVE file")
	}
	var formatOK bool
	var pcm []byte
	for offset := 12; offset+8 <= len(wav); {
		chunkSize := int(binary.LittleEndian.Uint32(wav[offset+4 : offset+8]))
		dataStart := offset + 8
		dataEnd := dataStart + chunkSize
		if chunkSize < 0 || dataEnd < dataStart || dataEnd > len(wav) {
			return nil, errors.New("recorded voice contains a truncated WAV chunk")
		}
		switch string(wav[offset : offset+4]) {
		case "fmt ":
			if chunkSize < 16 {
				return nil, errors.New("recorded voice has an invalid fmt chunk")
			}
			format := wav[dataStart:dataEnd]
			audioFormat := binary.LittleEndian.Uint16(format[0:2])
			channels := binary.LittleEndian.Uint16(format[2:4])
			sampleRate := int(binary.LittleEndian.Uint32(format[4:8]))
			blockAlign := binary.LittleEndian.Uint16(format[12:14])
			bitsPerSample := binary.LittleEndian.Uint16(format[14:16])
			formatOK = audioFormat == 1 && channels == 1 && sampleRate == expectedSampleRate &&
				blockAlign == 2 && bitsPerSample == 16
		case "data":
			pcm = append([]byte(nil), wav[dataStart:dataEnd]...)
		}
		offset = dataEnd
		if chunkSize%2 != 0 {
			offset++
		}
	}
	if !formatOK {
		return nil, errors.New("recorded voice must be 16 kHz mono PCM16 WAV")
	}
	if len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > maxPCMBytes {
		return nil, errors.New("recorded voice has invalid PCM sample data")
	}
	return pcm, nil
}
