package volcenginertc

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestRoomTokenSignerMatchesOfficialWireFormat(t *testing.T) {
	clockTime := time.Date(2026, time.July, 23, 8, 0, 0, 987654321, time.UTC)
	issuedAt := clockTime.Truncate(time.Second)
	signer, err := NewRoomTokenSigner(RoomTokenConfig{
		AppID:  "123456781234567812345678",
		AppKey: "app key",
		TTL:    2 * time.Hour,
	})
	if err != nil {
		t.Fatalf("new room token signer: %v", err)
	}
	if signer.AppID() != "123456781234567812345678" {
		t.Fatalf("AppID = %q", signer.AppID())
	}
	signer.now = func() time.Time { return clockTime }
	signer.random = bytes.NewReader([]byte{0x04, 0x03, 0x02, 0x01})

	signed, err := signer.Sign("room_1", "user_1")
	if err != nil {
		t.Fatalf("sign room token: %v", err)
	}
	if signed.ExpiresAt != issuedAt.Add(2*time.Hour) {
		t.Fatalf("expires at = %s", signed.ExpiresAt)
	}
	const officialNodeToken = "001123456781234567812345678PAAEAwIBAMphaiDmYWoGAHJvb21fMQYAdXNlcl8xBQAAACDmYWoBACDmYWoCACDmYWoDACDmYWoEACDmYWogAH2Abc2RqGxUiKo3THh8a0VBZXtZ93pLZIvkFvs1jHOG"
	if signed.Value != officialNodeToken {
		t.Fatalf("token = %q, want official fixture %q", signed.Value, officialNodeToken)
	}

	parsed := parseRoomTokenForTest(t, signed.Value, "app key")
	if parsed.version != "001" || parsed.appID != "123456781234567812345678" {
		t.Fatalf("identity = version %q app %q", parsed.version, parsed.appID)
	}
	if parsed.nonce != 0x01020304 ||
		parsed.issuedAt != uint32(issuedAt.Unix()) ||
		parsed.expireAt != uint32(signed.ExpiresAt.Unix()) ||
		parsed.roomID != "room_1" ||
		parsed.userID != "user_1" {
		t.Fatalf("token payload = %+v", parsed)
	}
	wantPrivileges := map[uint16]uint32{
		0: parsed.expireAt,
		1: parsed.expireAt,
		2: parsed.expireAt,
		3: parsed.expireAt,
		4: parsed.expireAt,
	}
	if len(parsed.privileges) != len(wantPrivileges) {
		t.Fatalf("privileges = %v", parsed.privileges)
	}
	for privilege, wantExpiry := range wantPrivileges {
		if got := parsed.privileges[privilege]; got != wantExpiry {
			t.Fatalf("privilege %d expiry = %d, want %d", privilege, got, wantExpiry)
		}
	}
}

func TestRoomTokenSignerNilAppIDIsEmpty(t *testing.T) {
	var signer *RoomTokenSigner
	if signer.AppID() != "" {
		t.Fatalf("nil signer AppID = %q", signer.AppID())
	}
}

func TestRoomTokenSignerRejectsInvalidConfigurationAndIdentity(t *testing.T) {
	configCases := []struct {
		name   string
		config RoomTokenConfig
		want   string
	}{
		{name: "missing app id", config: RoomTokenConfig{AppKey: "key", TTL: time.Hour}, want: "AppID"},
		{name: "invalid app id length", config: RoomTokenConfig{AppID: "short", AppKey: "key", TTL: time.Hour}, want: "24 bytes"},
		{name: "missing app key", config: RoomTokenConfig{AppID: "123456781234567812345678", TTL: time.Hour}, want: "AppKey"},
		{name: "missing ttl", config: RoomTokenConfig{AppID: "123456781234567812345678", AppKey: "key"}, want: "TTL"},
		{name: "subsecond ttl", config: RoomTokenConfig{AppID: "123456781234567812345678", AppKey: "key", TTL: time.Hour + time.Nanosecond}, want: "whole seconds"},
		{name: "long ttl", config: RoomTokenConfig{AppID: "123456781234567812345678", AppKey: "key", TTL: 2*time.Hour + time.Second}, want: "2h"},
	}
	for _, testCase := range configCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewRoomTokenSigner(testCase.config)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}

	signer, err := NewRoomTokenSigner(RoomTokenConfig{
		AppID:  "123456781234567812345678",
		AppKey: "key",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("new room token signer: %v", err)
	}
	for _, testCase := range []struct {
		name   string
		roomID string
		userID string
	}{
		{name: "empty room", userID: "user_1"},
		{name: "wildcard room", roomID: "*", userID: "user_1"},
		{name: "room punctuation", roomID: "room/1", userID: "user_1"},
		{name: "empty user", roomID: "room_1"},
		{name: "user punctuation", roomID: "room_1", userID: "user:1"},
		{name: "long room", roomID: strings.Repeat("r", 129), userID: "user_1"},
		{name: "long user", roomID: "room_1", userID: strings.Repeat("u", 129)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := signer.Sign(testCase.roomID, testCase.userID); err == nil {
				t.Fatal("invalid identity was accepted")
			}
		})
	}
}

func TestRoomTokenSignerAcceptsHyphenatedIdentity(t *testing.T) {
	signer, err := NewRoomTokenSigner(RoomTokenConfig{
		AppID:  "123456781234567812345678",
		AppKey: "key",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("new room token signer: %v", err)
	}

	if _, err := signer.Sign("voice-call-1", "member-1"); err != nil {
		t.Fatalf("sign hyphenated identity: %v", err)
	}
}

func TestRoomTokenSignerPropagatesSecureRandomFailure(t *testing.T) {
	signer, err := NewRoomTokenSigner(RoomTokenConfig{
		AppID:  "123456781234567812345678",
		AppKey: "key",
		TTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("new room token signer: %v", err)
	}
	signer.random = errReader{err: errors.New("entropy unavailable")}

	if _, err := signer.Sign("room_1", "user_1"); err == nil ||
		!strings.Contains(err.Error(), "generate RTC token nonce") {
		t.Fatalf("error = %v, want nonce generation failure", err)
	}
}

type parsedRoomToken struct {
	version    string
	appID      string
	nonce      uint32
	issuedAt   uint32
	expireAt   uint32
	roomID     string
	userID     string
	privileges map[uint16]uint32
}

func parseRoomTokenForTest(t *testing.T, token, appKey string) parsedRoomToken {
	t.Helper()
	if len(token) <= roomTokenVersionLength+roomTokenAppIDLength {
		t.Fatalf("token is too short: %d", len(token))
	}
	parsed := parsedRoomToken{
		version:    token[:roomTokenVersionLength],
		appID:      token[roomTokenVersionLength : roomTokenVersionLength+roomTokenAppIDLength],
		privileges: make(map[uint16]uint32),
	}
	content, err := base64.StdEncoding.DecodeString(token[roomTokenVersionLength+roomTokenAppIDLength:])
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	contentReader := bytes.NewReader(content)
	message := readLengthPrefixedBytesForTest(t, contentReader)
	signature := readLengthPrefixedBytesForTest(t, contentReader)
	if contentReader.Len() != 0 {
		t.Fatalf("unexpected trailing content: %d bytes", contentReader.Len())
	}
	mac := hmac.New(sha256.New, []byte(appKey))
	_, _ = mac.Write(message)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("token signature does not match payload")
	}

	messageReader := bytes.NewReader(message)
	parsed.nonce = readUint32ForTest(t, messageReader)
	parsed.issuedAt = readUint32ForTest(t, messageReader)
	parsed.expireAt = readUint32ForTest(t, messageReader)
	parsed.roomID = string(readLengthPrefixedBytesForTest(t, messageReader))
	parsed.userID = string(readLengthPrefixedBytesForTest(t, messageReader))
	privilegeCount := readUint16ForTest(t, messageReader)
	for range privilegeCount {
		privilege := readUint16ForTest(t, messageReader)
		parsed.privileges[privilege] = readUint32ForTest(t, messageReader)
	}
	if messageReader.Len() != 0 {
		t.Fatalf("unexpected trailing message: %d bytes", messageReader.Len())
	}
	return parsed
}

func readLengthPrefixedBytesForTest(t *testing.T, reader *bytes.Reader) []byte {
	t.Helper()
	length := readUint16ForTest(t, reader)
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		t.Fatalf("read %d bytes: %v", length, err)
	}
	return value
}

func readUint16ForTest(t *testing.T, reader io.Reader) uint16 {
	t.Helper()
	var value uint16
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		t.Fatalf("read uint16: %v", err)
	}
	return value
}

func readUint32ForTest(t *testing.T, reader io.Reader) uint32 {
	t.Helper()
	var value uint32
	if err := binary.Read(reader, binary.LittleEndian, &value); err != nil {
		t.Fatalf("read uint32: %v", err)
	}
	return value
}

type errReader struct {
	err error
}

func (reader errReader) Read([]byte) (int, error) {
	return 0, reader.err
}
