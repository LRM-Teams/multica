package voiceaudio

import (
	"encoding/binary"
	"errors"
)

// EncodePCM16MonoWAV wraps complete signed 16-bit mono samples in a
// self-describing WAV container. Output size policy belongs to the caller:
// inbound recordings and provider-generated speech have different limits.
func EncodePCM16MonoWAV(pcm []byte, sampleRate int) ([]byte, int64, error) {
	if sampleRate <= 0 || len(pcm) == 0 || len(pcm)%2 != 0 {
		return nil, 0, errors.New("invalid PCM audio")
	}
	const headerBytes = 44
	wav := make([]byte, headerBytes+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], 1)
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(wav[32:34], 2)
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	durationMS := (int64(len(pcm)/2)*1000 + int64(sampleRate)/2) / int64(sampleRate)
	return wav, durationMS, nil
}

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
