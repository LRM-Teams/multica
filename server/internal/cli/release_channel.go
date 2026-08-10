package cli

import (
	"fmt"
	"strings"
)

type ReleaseChannel string

const (
	ReleaseChannelLatest ReleaseChannel = "latest"
	ReleaseChannelAlpha  ReleaseChannel = "alpha"
)

func NormalizeReleaseChannel(raw string) (ReleaseChannel, error) {
	channel := ReleaseChannel(strings.ToLower(strings.TrimSpace(raw)))
	if channel == "" {
		channel = ReleaseChannelLatest
	}
	switch channel {
	case ReleaseChannelLatest, ReleaseChannelAlpha:
		return channel, nil
	default:
		return "", fmt.Errorf("unsupported release channel %q: use latest or alpha", raw)
	}
}

func DefaultReleaseChannelForEnvironment(environment ServiceEnvironment) ReleaseChannel {
	if environment == ServiceEnvironmentTest {
		return ReleaseChannelAlpha
	}
	return ReleaseChannelLatest
}

// ResolveReleaseChannel applies the environment-specific default only when a
// config has never made an explicit channel choice. Once persisted, channel
// and environment remain independent axes.
func ResolveReleaseChannel(cfg CLIConfig) (ReleaseChannel, error) {
	if strings.TrimSpace(cfg.ReleaseChannel) != "" {
		return NormalizeReleaseChannel(cfg.ReleaseChannel)
	}
	target, err := ResolveServiceTarget(cfg)
	if err != nil {
		return "", err
	}
	return DefaultReleaseChannelForEnvironment(target.Environment), nil
}
