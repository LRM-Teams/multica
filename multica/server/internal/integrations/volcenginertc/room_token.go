package volcenginertc

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	roomTokenVersion       = "001"
	roomTokenVersionLength = len(roomTokenVersion)
	roomTokenAppIDLength   = 24
	roomTokenMaxIDLength   = 128
	roomTokenMaxTTL        = 2 * time.Hour
	roomTokenMaxUnix       = int64(1<<32 - 1)

	roomTokenPrivilegePublishStream      uint16 = 0
	roomTokenPrivilegePublishAudioStream uint16 = 1
	roomTokenPrivilegePublishVideoStream uint16 = 2
	roomTokenPrivilegePublishDataStream  uint16 = 3
	roomTokenPrivilegeSubscribeStream    uint16 = 4
)

type RoomTokenConfig struct {
	AppID  string
	AppKey string
	TTL    time.Duration
}

type SignedRoomToken struct {
	Value     string
	ExpiresAt time.Time
}

type RoomTokenSigner struct {
	appID  string
	appKey string
	ttl    time.Duration
	now    func() time.Time
	random io.Reader
}

func NewRoomTokenSigner(config RoomTokenConfig) (*RoomTokenSigner, error) {
	if config.AppID == "" {
		return nil, errors.New("volcengine RTC AppID is required")
	}
	if len(config.AppID) != roomTokenAppIDLength {
		return nil, fmt.Errorf("volcengine RTC AppID must be exactly %d bytes", roomTokenAppIDLength)
	}
	if config.AppKey == "" {
		return nil, errors.New("volcengine RTC AppKey is required")
	}
	if config.TTL <= 0 {
		return nil, errors.New("volcengine RTC token TTL must be positive")
	}
	if config.TTL%time.Second != 0 {
		return nil, errors.New("volcengine RTC token TTL must use whole seconds")
	}
	if config.TTL > roomTokenMaxTTL {
		return nil, fmt.Errorf("volcengine RTC token TTL must not exceed %s", roomTokenMaxTTL)
	}
	return &RoomTokenSigner{
		appID:  config.AppID,
		appKey: config.AppKey,
		ttl:    config.TTL,
		now:    time.Now,
		random: rand.Reader,
	}, nil
}

func (signer *RoomTokenSigner) AppID() string {
	if signer == nil {
		return ""
	}
	return signer.appID
}

func (signer *RoomTokenSigner) Sign(roomID, userID string) (SignedRoomToken, error) {
	if signer == nil || signer.now == nil || signer.random == nil {
		return SignedRoomToken{}, errors.New("volcengine RTC room token signer is not initialized")
	}
	if err := validateRoomTokenID("RoomID", roomID); err != nil {
		return SignedRoomToken{}, err
	}
	if roomID == "*" {
		return SignedRoomToken{}, errors.New("volcengine RTC RoomID must be room-scoped")
	}
	if err := validateRoomTokenID("UserID", userID); err != nil {
		return SignedRoomToken{}, err
	}

	issuedAt := signer.now().UTC().Truncate(time.Second)
	expiresAt := issuedAt.Add(signer.ttl)
	issuedUnix := issuedAt.Unix()
	expiresUnix := expiresAt.Unix()
	if issuedUnix < 0 || issuedUnix > roomTokenMaxUnix ||
		expiresUnix < 0 || expiresUnix > roomTokenMaxUnix {
		return SignedRoomToken{}, errors.New("volcengine RTC token time is outside the supported range")
	}

	var nonceBytes [4]byte
	if _, err := io.ReadFull(signer.random, nonceBytes[:]); err != nil {
		return SignedRoomToken{}, fmt.Errorf("generate RTC token nonce: %w", err)
	}
	nonce := binary.LittleEndian.Uint32(nonceBytes[:])
	expiry := uint32(expiresUnix)

	message := new(bytes.Buffer)
	if err := binary.Write(message, binary.LittleEndian, nonce); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token nonce: %w", err)
	}
	if err := binary.Write(message, binary.LittleEndian, uint32(issuedUnix)); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token issue time: %w", err)
	}
	if err := binary.Write(message, binary.LittleEndian, expiry); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token expiry: %w", err)
	}
	if err := writeRoomTokenBytes(message, []byte(roomID)); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC RoomID: %w", err)
	}
	if err := writeRoomTokenBytes(message, []byte(userID)); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC UserID: %w", err)
	}
	privileges := []uint16{
		roomTokenPrivilegePublishStream,
		roomTokenPrivilegePublishAudioStream,
		roomTokenPrivilegePublishVideoStream,
		roomTokenPrivilegePublishDataStream,
		roomTokenPrivilegeSubscribeStream,
	}
	if err := binary.Write(message, binary.LittleEndian, uint16(len(privileges))); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token privilege count: %w", err)
	}
	for _, privilege := range privileges {
		if err := binary.Write(message, binary.LittleEndian, privilege); err != nil {
			return SignedRoomToken{}, fmt.Errorf("encode RTC token privilege: %w", err)
		}
		if err := binary.Write(message, binary.LittleEndian, expiry); err != nil {
			return SignedRoomToken{}, fmt.Errorf("encode RTC token privilege expiry: %w", err)
		}
	}

	mac := hmac.New(sha256.New, []byte(signer.appKey))
	if _, err := mac.Write(message.Bytes()); err != nil {
		return SignedRoomToken{}, fmt.Errorf("sign RTC token: %w", err)
	}
	content := new(bytes.Buffer)
	if err := writeRoomTokenBytes(content, message.Bytes()); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token payload: %w", err)
	}
	if err := writeRoomTokenBytes(content, mac.Sum(nil)); err != nil {
		return SignedRoomToken{}, fmt.Errorf("encode RTC token signature: %w", err)
	}

	return SignedRoomToken{
		Value:     roomTokenVersion + signer.appID + base64.StdEncoding.EncodeToString(content.Bytes()),
		ExpiresAt: expiresAt,
	}, nil
}

func validateRoomTokenID(field, value string) error {
	if value == "" {
		return fmt.Errorf("volcengine RTC %s is required", field)
	}
	if len(value) > roomTokenMaxIDLength {
		return fmt.Errorf("volcengine RTC %s must not exceed %d characters", field, roomTokenMaxIDLength)
	}
	for _, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '@' || character == '_' || character == '-' || character == '.' {
			continue
		}
		return fmt.Errorf("volcengine RTC %s contains an unsupported character", field)
	}
	return nil
}

func writeRoomTokenBytes(writer io.Writer, value []byte) error {
	if len(value) > int(^uint16(0)) {
		return errors.New("value exceeds uint16 length")
	}
	if err := binary.Write(writer, binary.LittleEndian, uint16(len(value))); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}
