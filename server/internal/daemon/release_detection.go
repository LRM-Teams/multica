package daemon

import (
	"context"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// releaseDetectionLoop periodically checks the release feed for a newer CLI version
// and, when one exists, records it as the auto-detected target on the daemon's
// update observation (which the heartbeat already reports to the server, so
// the frontend can surface the "upgrade available" affordance).
//
// This is detection-only: it never mutates a machine's release state and never
// installs anything. Installation remains an explicit Machine Upgrade operation
// (the button), matching Frank's requirement "不自动更新, 但是得自动检测".
//
// The loop is intentionally cheap (a single GET of the release manifest every
// interval) and resilient: any transient fetch/parse error is logged and the
// next tick retries; a fetch failure never blocks message delivery or agent
// work.
func (d *Daemon) releaseDetectionLoop(ctx context.Context) {
	if d.cfg.ReleaseDetectionInterval <= 0 {
		return
	}

	// A short initial delay lets the daemon finish the most time-sensitive
	// startup (registration, heartbeat, mailbox drain) before the first check;
	// afterwards it repeats on the configured interval.
	timer := time.NewTimer(d.releaseDetectionInitialDelay())
	defer timer.Stop()

	ticker := time.NewTicker(d.cfg.ReleaseDetectionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			d.checkForNewerRelease(ctx)
		case <-ticker.C:
			d.checkForNewerRelease(ctx)
		}
	}
}

// releaseDetectionInitialDelay is the delay before the first detection check. It is a
// method so tests can shorten it.
func (d *Daemon) releaseDetectionInitialDelay() time.Duration {
	if d.cfg.ReleaseDetectionInitialDelay > 0 {
		return d.cfg.ReleaseDetectionInitialDelay
	}
	return 2 * time.Minute
}

// checkForNewerRelease fetches the release manifest and, if it advertises a
// release newer than the running CLI version, records it as the detected
// target on the daemon's update observation. No install is performed.
func (d *Daemon) checkForNewerRelease(ctx context.Context) {
	if d.updateObservation == nil {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
		obs.Phase = "checking"
		obs.AttemptSource = "auto"
		obs.LastAttemptAt = now
		obs.ErrorCode = ""
		obs.ErrorMessage = ""
	}); err != nil {
		if d.logger != nil {
			d.logger.Warn("release detection: could not persist checking state", "error", err)
		}
		return
	}
	manifest, err := fetchReleaseManifestForChannel(ctx, d.cfg.ReleaseChannel, d.releaseManifestBaseURLOverride())
	if err != nil {
		transitionErr := d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
			obs.Phase = "waiting"
			obs.LastOutcome = "fetch_failed"
			obs.ErrorCode = "release_fetch_failed"
			obs.ErrorMessage = err.Error()
		})
		if d.logger != nil {
			d.logger.Debug("release detection: manifest fetch failed", "error", err)
			if transitionErr != nil {
				d.logger.Warn("release detection: could not persist fetch failure", "error", transitionErr)
			}
		}
		return
	}
	latest := manifest.Version
	if latest == "" {
		latest = manifest.TagName
	}
	if !cli.IsReleaseVersion(latest) {
		transitionErr := d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
			obs.Phase = "waiting"
			obs.LastOutcome = "fetch_failed"
			obs.ErrorCode = "release_fetch_failed"
			obs.ErrorMessage = "release manifest did not contain a valid release version"
		})
		if d.logger != nil {
			d.logger.Debug("release detection: manifest version not a release tag", "version", latest)
			if transitionErr != nil {
				d.logger.Warn("release detection: could not persist invalid manifest", "error", transitionErr)
			}
		}
		return
	}
	current := d.cfg.CLIVersion
	if !cli.IsNewerVersion(latest, current) {
		// No newer release (or equal/older): clear any stale detected target so
		// the frontend doesn't keep offering a version that's no longer newer.
		clearStaleTarget := d.hasStaleDetectedTarget(current)
		if err := d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
			obs.Phase = "waiting"
			obs.LastOutcome = "up_to_date"
			obs.ErrorCode = ""
			obs.ErrorMessage = ""
			if clearStaleTarget {
				obs.TargetVersion = ""
			}
		}); err != nil && d.logger != nil {
			d.logger.Warn("release detection: could not persist up-to-date state", "error", err)
		}
		return
	}
	target := cli.NormalizeReleaseTag(latest)
	if target == "" {
		target = latest
	}
	if err := d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
		obs.Phase = "waiting"
		obs.LastOutcome = "update_available"
		obs.TargetVersion = target
		obs.ErrorCode = ""
		obs.ErrorMessage = ""
	}); err != nil {
		if d.logger != nil {
			d.logger.Warn("release detection: could not persist detected release", "target", target, "error", err)
		}
		return
	}
	if d.logger != nil {
		d.logger.Info("release detection: newer release found", "current", current, "target", target)
	}
}

// hasStaleDetectedTarget reports whether the current observation still carries
// a detected target that is no longer newer than current (so we can clear it).
func (d *Daemon) hasStaleDetectedTarget(current string) bool {
	obs := d.updateObservation.Snapshot()
	if obs.TargetVersion == "" {
		return false
	}
	// Only clear a target we would have detected ourselves (a release tag).
	if !cli.IsReleaseVersion(obs.TargetVersion) {
		return false
	}
	return !cli.IsNewerVersion(obs.TargetVersion, current)
}

// fetchReleaseManifest returns the Production entry from the canonical
// metainfo document. Release selection belongs to cli so installers, explicit
// Machine Upgrades, and detection cannot silently drift onto different files.
func fetchReleaseManifest(ctx context.Context) (*cli.ReleaseManifest, error) {
	return fetchReleaseManifestForChannel(ctx, string(cli.ReleaseChannelLatest), "")
}

func fetchReleaseManifestForChannel(ctx context.Context, rawChannel, serverDispatched string) (*cli.ReleaseManifest, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	channel, err := cli.NormalizeReleaseChannel(rawChannel)
	if err != nil {
		return nil, err
	}
	return cli.FetchReleaseForChannelWithOverride(channel, serverDispatched)
}
