package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	defaultTTL       = 4 * time.Hour
	defaultUrgency   = "normal"
	maxRecordSize    = 4096
	contentEncoding  = "aes128gcm"
	webPushInfoLabel = "WebPush: info\x00"
)

type Subscription struct {
	Endpoint string
	P256DH   string
	Auth     string
}

type Config struct {
	PublicKey  string
	PrivateKey string
	Subject    string
	TTL        time.Duration
	Client     *http.Client
}

type Sender struct {
	publicKey  string
	privateKey string
	subject    string
	ttl        time.Duration
	client     *http.Client
}

type Result struct {
	StatusCode int
	Gone       bool
}

func NewSender(cfg Config) *Sender {
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultTTL
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &Sender{
		publicKey:  strings.TrimSpace(cfg.PublicKey),
		privateKey: strings.TrimSpace(cfg.PrivateKey),
		subject:    strings.TrimSpace(cfg.Subject),
		ttl:        ttl,
		client:     client,
	}
}

func (s *Sender) Enabled() bool {
	return s.publicKey != "" && s.privateKey != "" && s.subject != ""
}

func (s *Sender) Send(ctx context.Context, sub Subscription, payload any) (Result, error) {
	if !s.Enabled() {
		return Result{}, errors.New("web push sender is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, err
	}
	ciphertext, _, _, err := encryptPayload(body, sub)
	if err != nil {
		return Result{}, err
	}
	endpointURL, err := url.Parse(sub.Endpoint)
	if err != nil {
		return Result{}, err
	}
	vapidKey, err := decodePrivateKey(s.privateKey)
	if err != nil {
		return Result{}, err
	}
	audience := endpointURL.Scheme + "://" + endpointURL.Host
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"aud": audience,
		"exp": time.Now().Add(12 * time.Hour).Unix(),
		"sub": s.subject,
	})
	signed, err := token.SignedString(vapidKey)
	if err != nil {
		return Result{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(ciphertext))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", contentEncoding)
	req.Header.Set("TTL", fmt.Sprintf("%d", int(s.ttl.Seconds())))
	req.Header.Set("Urgency", defaultUrgency)
	req.Header.Set("Authorization", "vapid t="+signed+", k="+s.publicKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	result := Result{StatusCode: resp.StatusCode, Gone: resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return result, nil
	}
	return result, fmt.Errorf("web push service returned %d", resp.StatusCode)
}

func encryptPayload(payload []byte, sub Subscription) ([]byte, []byte, []byte, error) {
	clientPublicBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(sub.P256DH))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode p256dh: %w", err)
	}
	clientAuth, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(sub.Auth))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode auth: %w", err)
	}
	clientX, clientY := elliptic.Unmarshal(elliptic.P256(), clientPublicBytes)
	if clientX == nil || clientY == nil {
		return nil, nil, nil, errors.New("invalid p256dh key")
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	sharedX, _ := elliptic.P256().ScalarMult(clientX, clientY, serverKey.D.Bytes())
	sharedSecret := padTo32(sharedX.Bytes())
	serverPublic := elliptic.Marshal(elliptic.P256(), serverKey.PublicKey.X, serverKey.PublicKey.Y)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, nil, err
	}
	keyInfo := append([]byte(webPushInfoLabel), clientPublicBytes...)
	keyInfo = append(keyInfo, serverPublic...)
	ikm := hkdfSHA256(clientAuth, sharedSecret, keyInfo, 32)
	cek := hkdfSHA256(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdfSHA256(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	record := make([]byte, 0, len(payload)+1)
	record = append(record, payload...)
	record = append(record, 0x02)
	if len(record) > maxRecordSize {
		return nil, nil, nil, errors.New("web push payload too large")
	}
	block, err := newAESGCM(cek)
	if err != nil {
		return nil, nil, nil, err
	}
	header := make([]byte, 0, 16+4+1+len(serverPublic))
	header = append(header, salt...)
	header = append(header, byte(maxRecordSize>>24), byte(maxRecordSize>>16), byte(maxRecordSize>>8), byte(maxRecordSize&0xff))
	header = append(header, byte(len(serverPublic)))
	header = append(header, serverPublic...)
	return append(header, block.Seal(nil, nonce, record, nil)...), salt, serverPublic, nil
}

func decodePrivateKey(raw string) (*ecdsa.PrivateKey, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(b) != 32 {
		return nil, errors.New("VAPID private key must be 32 bytes")
	}
	curve := elliptic.P256()
	d := new(big.Int).SetBytes(b)
	if d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, errors.New("VAPID private key is outside P-256 range")
	}
	x, y := curve.ScalarBaseMult(b)
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}, nil
}

func padTo32(in []byte) []byte {
	if len(in) >= 32 {
		return in
	}
	out := make([]byte, 32)
	copy(out[32-len(in):], in)
	return out
}

func hkdfSHA256(salt, ikm, info []byte, length int) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	prk := mac.Sum(nil)

	var out []byte
	var previous []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac = hmac.New(sha256.New, prk)
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		out = append(out, previous...)
	}
	return out[:length]
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
