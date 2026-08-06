package doubaospeech

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	protocolVersion byte = 1
	headerWords     byte = 1

	messageFullClient  byte = 1
	messageAudioClient byte = 2
	messageFullServer  byte = 9
	messageAudioServer byte = 11
	messageError       byte = 15

	flagPositiveSequence byte = 1
	flagNegativeSequence byte = 2
	flagSequence         byte = 3
	flagEvent            byte = 4

	serializationNone byte = 0
	serializationJSON byte = 1

	compressionNone byte = 0
	compressionGZIP byte = 1

	maxProviderFrameBytes = 16 << 20
)

const (
	eventStartConnection    int32 = 1
	eventFinishConnection   int32 = 2
	eventConnectionStarted  int32 = 50
	eventConnectionFailed   int32 = 51
	eventConnectionFinished int32 = 52
	eventStartSession       int32 = 100
	eventFinishSession      int32 = 102
	eventSessionStarted     int32 = 150
	eventSessionFinished    int32 = 152
	eventSessionFailed      int32 = 153
	eventTaskRequest        int32 = 200
	eventTTSResponse        int32 = 352
)

var errMalformedFrame = errors.New("doubao speech: malformed provider frame")

type providerFrame struct {
	messageType   byte
	flags         byte
	serialization byte
	compression   byte
	sequence      int32
	event         int32
	sessionID     string
	connectionID  string
	errorCode     uint32
	payload       []byte
	last          bool
}

func frameHeader(messageType, flags, serialization, compression byte) []byte {
	return []byte{
		(protocolVersion << 4) | headerWords,
		(messageType << 4) | flags,
		(serialization << 4) | compression,
		0,
	}
}

func appendUint32(dst []byte, value uint32) []byte {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], value)
	return append(dst, raw[:]...)
}

func appendInt32(dst []byte, value int32) []byte {
	return appendUint32(dst, uint32(value))
}

func appendSizedBytes(dst, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func gzipBytes(value []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(value); err != nil {
		return nil, fmt.Errorf("compress payload: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finish payload compression: %w", err)
	}
	return out.Bytes(), nil
}

func gunzipBytes(value []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(value))
	if err != nil {
		return nil, fmt.Errorf("open compressed payload: %w", err)
	}
	decompressed, err := io.ReadAll(io.LimitReader(zr, maxProviderFrameBytes+1))
	closeErr := zr.Close()
	if err != nil {
		return nil, fmt.Errorf("read compressed payload: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close compressed payload: %w", closeErr)
	}
	if len(decompressed) > maxProviderFrameBytes {
		return nil, fmt.Errorf("%w: decompressed payload exceeds %d bytes", errMalformedFrame, maxProviderFrameBytes)
	}
	return decompressed, nil
}

func marshalASRFullRequest(sequence int32, payload []byte) ([]byte, error) {
	compressed, err := gzipBytes(payload)
	if err != nil {
		return nil, err
	}
	frame := frameHeader(messageFullClient, flagPositiveSequence, serializationJSON, compressionGZIP)
	frame = appendInt32(frame, sequence)
	return appendSizedBytes(frame, compressed), nil
}

func marshalASRAudio(sequence int32, last bool, payload []byte) ([]byte, error) {
	compressed, err := gzipBytes(payload)
	if err != nil {
		return nil, err
	}
	flags := flagPositiveSequence
	if last {
		flags = flagSequence
		sequence = -sequence
	}
	frame := frameHeader(messageAudioClient, flags, serializationNone, compressionGZIP)
	frame = appendInt32(frame, sequence)
	return appendSizedBytes(frame, compressed), nil
}

func marshalTTSEvent(event int32, sessionID string, payload []byte) []byte {
	frame := frameHeader(messageFullClient, flagEvent, serializationJSON, compressionNone)
	frame = appendInt32(frame, event)
	if !isConnectionEvent(event) {
		frame = appendSizedBytes(frame, []byte(sessionID))
	}
	return appendSizedBytes(frame, payload)
}

func parseProviderFrame(raw []byte) (providerFrame, error) {
	if len(raw) < 4 {
		return providerFrame{}, fmt.Errorf("%w: header is truncated", errMalformedFrame)
	}
	if raw[0]>>4 != protocolVersion {
		return providerFrame{}, fmt.Errorf("%w: unsupported version %d", errMalformedFrame, raw[0]>>4)
	}
	headerSize := int(raw[0]&0x0f) * 4
	if headerSize < 4 || len(raw) < headerSize {
		return providerFrame{}, fmt.Errorf("%w: invalid header size %d", errMalformedFrame, headerSize)
	}

	frame := providerFrame{
		messageType:   raw[1] >> 4,
		flags:         raw[1] & 0x0f,
		serialization: raw[2] >> 4,
		compression:   raw[2] & 0x0f,
	}
	cursor := headerSize
	readInt32 := func() (int32, error) {
		if len(raw)-cursor < 4 {
			return 0, fmt.Errorf("%w: int32 is truncated", errMalformedFrame)
		}
		value := int32(binary.BigEndian.Uint32(raw[cursor : cursor+4]))
		cursor += 4
		return value, nil
	}
	readUint32 := func() (uint32, error) {
		value, err := readInt32()
		return uint32(value), err
	}
	readSized := func() ([]byte, error) {
		size, err := readUint32()
		if err != nil {
			return nil, err
		}
		if size > maxProviderFrameBytes || uint64(size) > uint64(len(raw)-cursor) {
			return nil, fmt.Errorf("%w: invalid payload size %d", errMalformedFrame, size)
		}
		value := raw[cursor : cursor+int(size)]
		cursor += int(size)
		return value, nil
	}

	if frame.messageType == messageError {
		code, err := readUint32()
		if err != nil {
			return providerFrame{}, err
		}
		frame.errorCode = code
		payload, err := readSized()
		if err != nil {
			return providerFrame{}, err
		}
		frame.payload = payload
		if cursor != len(raw) {
			return providerFrame{}, fmt.Errorf("%w: %d trailing bytes", errMalformedFrame, len(raw)-cursor)
		}
		return decompressProviderFrame(frame)
	}

	if frame.flags&flagPositiveSequence != 0 {
		sequence, err := readInt32()
		if err != nil {
			return providerFrame{}, err
		}
		frame.sequence = sequence
	}
	frame.last = frame.flags&flagNegativeSequence != 0

	if frame.flags&flagEvent != 0 {
		event, err := readInt32()
		if err != nil {
			return providerFrame{}, err
		}
		frame.event = event
		if !isConnectionEvent(event) {
			sessionID, err := readSized()
			if err != nil {
				return providerFrame{}, err
			}
			frame.sessionID = string(sessionID)
		}
		if event == eventConnectionStarted || event == eventConnectionFailed || event == eventConnectionFinished {
			connectionID, err := readSized()
			if err != nil {
				return providerFrame{}, err
			}
			frame.connectionID = string(connectionID)
		}
	}

	payload, err := readSized()
	if err != nil {
		return providerFrame{}, err
	}
	frame.payload = payload
	if cursor != len(raw) {
		return providerFrame{}, fmt.Errorf("%w: %d trailing bytes", errMalformedFrame, len(raw)-cursor)
	}
	return decompressProviderFrame(frame)
}

func decompressProviderFrame(frame providerFrame) (providerFrame, error) {
	switch frame.compression {
	case compressionNone:
		return frame, nil
	case compressionGZIP:
		payload, err := gunzipBytes(frame.payload)
		if err != nil {
			return providerFrame{}, err
		}
		frame.payload = payload
		return frame, nil
	default:
		return providerFrame{}, fmt.Errorf("%w: unsupported compression %d", errMalformedFrame, frame.compression)
	}
}

func isConnectionEvent(event int32) bool {
	return event == eventStartConnection || event == eventFinishConnection ||
		event == eventConnectionStarted || event == eventConnectionFailed || event == eventConnectionFinished
}
