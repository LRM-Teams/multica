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

// ReleaseChannelForEnvironment is the fixed package source for one service
// environment. It is deliberately not user-configurable: production always
// runs stable packages and test always runs preview packages.
func ReleaseChannelForEnvironment(environment ServiceEnvironment) ReleaseChannel {
	if environment == ServiceEnvironmentTest {
		return ReleaseChannelAlpha
	}
	return ReleaseChannelLatest
}

// ResolveReleaseChannel derives package source solely from the effective
// service environment. Legacy release_channel values in old config files are
// ignored during migration.
func ResolveReleaseChannel(cfg CLIConfig) (ReleaseChannel, error) {
	target, err := ResolveServiceTarget(cfg)
	if err != nil {
		return "", err
	}
	return ReleaseChannelForEnvironment(target.Environment), nil
}
