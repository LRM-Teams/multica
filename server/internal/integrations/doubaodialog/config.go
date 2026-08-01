package doubaodialog

import (
	"errors"
	"os"
	"strings"
	"time"
)

const (
	// DefaultDuplexEndpoint is the Seeduplex / Realtime Duplex JSON WebSocket.
	DefaultDuplexEndpoint = "wss://openspeech.bytedance.com/api/v3/duplex/realtime/dialogue"
	// ClassicDialogueEndpoint is the binary Realtime Dialogue product named in LRM-945.
	ClassicDialogueEndpoint = "wss://openspeech.bytedance.com/api/v3/realtime/dialogue"

	DefaultResourceID = "volc.speech.dialog"
	// ClassicAppKey is a fixed upstream handshake constant for the classic dialogue path.
	ClassicAppKey = "PlgvMymc7f3tQnJ6"

	DefaultModel = "1.2.6.0"
	DefaultVoice = "zh_female_vv_jupiter_bigtts"

	EnvAPIKey     = "DOUBAO_DIALOG_API_KEY"
	EnvAppID      = "DOUBAO_DIALOG_APP_ID"
	EnvAccessKey  = "DOUBAO_DIALOG_ACCESS_KEY"
	EnvEndpoint   = "DOUBAO_DIALOG_ENDPOINT"
	EnvResourceID = "DOUBAO_DIALOG_RESOURCE_ID"
	EnvVoice      = "DOUBAO_DIALOG_VOICE"
	EnvModel      = "DOUBAO_DIALOG_MODEL"
)

// Config holds non-secret defaults plus credentials loaded from env or callers.
// Never log AccessKey / APIKey values.
type Config struct {
	APIKey     string
	AppID      string
	AccessKey  string
	Endpoint   string
	ResourceID string
	Voice      string
	Model      string
	Timeout    time.Duration
}

func ConfigFromEnv() Config {
	endpoint := strings.TrimSpace(os.Getenv(EnvEndpoint))
	if endpoint == "" {
		endpoint = DefaultDuplexEndpoint
	}
	resourceID := strings.TrimSpace(os.Getenv(EnvResourceID))
	if resourceID == "" {
		resourceID = DefaultResourceID
	}
	voice := strings.TrimSpace(os.Getenv(EnvVoice))
	if voice == "" {
		voice = DefaultVoice
	}
	model := strings.TrimSpace(os.Getenv(EnvModel))
	if model == "" {
		model = DefaultModel
	}
	return Config{
		APIKey:     strings.TrimSpace(os.Getenv(EnvAPIKey)),
		AppID:      strings.TrimSpace(os.Getenv(EnvAppID)),
		AccessKey:  strings.TrimSpace(os.Getenv(EnvAccessKey)),
		Endpoint:   endpoint,
		ResourceID: resourceID,
		Voice:      voice,
		Model:      model,
		Timeout:    60 * time.Second,
	}
}

func (c Config) Normalized() Config {
	out := c
	out.APIKey = strings.TrimSpace(out.APIKey)
	out.AppID = strings.TrimSpace(out.AppID)
	out.AccessKey = strings.TrimSpace(out.AccessKey)
	out.Endpoint = strings.TrimSpace(out.Endpoint)
	out.ResourceID = strings.TrimSpace(out.ResourceID)
	out.Voice = strings.TrimSpace(out.Voice)
	out.Model = strings.TrimSpace(out.Model)
	if out.Endpoint == "" {
		out.Endpoint = DefaultDuplexEndpoint
	}
	if out.ResourceID == "" {
		out.ResourceID = DefaultResourceID
	}
	if out.Voice == "" {
		out.Voice = DefaultVoice
	}
	if out.Model == "" {
		out.Model = DefaultModel
	}
	if out.Timeout <= 0 {
		out.Timeout = 60 * time.Second
	}
	return out
}

func (c Config) IsDuplex() bool {
	endpoint := c.Normalized().Endpoint
	if strings.Contains(endpoint, "/duplex/") {
		return true
	}
	// Classic binary Realtime Dialogue path from LRM-945.
	if strings.Contains(endpoint, "/api/v3/realtime/dialogue") {
		return false
	}
	// Custom/test WebSocket URLs default to the Duplex JSON protocol.
	return true
}

func (c Config) ValidateForDial() error {
	cfg := c.Normalized()
	if cfg.IsDuplex() {
		if cfg.APIKey == "" {
			return errors.New(EnvAPIKey + " is required for Doubao Realtime Duplex")
		}
		return nil
	}
	if cfg.AppID == "" {
		return errors.New(EnvAppID + " is required for classic Realtime Dialogue")
	}
	if cfg.AccessKey == "" {
		return errors.New(EnvAccessKey + " is required for classic Realtime Dialogue")
	}
	return nil
}
