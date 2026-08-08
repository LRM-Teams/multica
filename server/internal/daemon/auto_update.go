package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// autoUpdateLoop periodically checks the release feed for a newer CLI version
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
func (d *Daemon) autoUpdateLoop(ctx context.Context) {
	if d.cfg.AutoUpdateCheckInterval <= 0 {
		return
	}

	// A short initial delay lets the daemon finish the most time-sensitive
	// startup (registration, heartbeat, mailbox drain) before the first check;
	// afterwards it repeats on the configured interval.
	timer := time.NewTimer(d.autoUpdateInitialDelay())
	defer timer.Stop()

	ticker := time.NewTicker(d.cfg.AutoUpdateCheckInterval)
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

// autoUpdateInitialDelay is the delay before the first detection check. It is a
// method so tests can shorten it.
func (d *Daemon) autoUpdateInitialDelay() time.Duration {
	if d.cfg.AutoUpdateInitialDelay > 0 {
		return d.cfg.AutoUpdateInitialDelay
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
	manifest, err := fetchReleaseManifest(ctx)
	if err != nil {
		d.logger.Debug("auto-update detection: manifest fetch failed", "error", err)
		return
	}
	latest := manifest.Version
	if latest == "" {
		latest = manifest.TagName
	}
	if !cli.IsReleaseVersion(latest) {
		d.logger.Debug("auto-update detection: manifest version not a release tag", "version", latest)
		return
	}
	current := d.cfg.CLIVersion
	if !cli.IsNewerVersion(latest, current) {
		// No newer release (or equal/older): clear any stale detected target so
		// the frontend doesn't keep offering a version that's no longer newer.
		if d.hasStaleDetectedTarget(current) {
			d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
				obs.TargetVersion = ""
				obs.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
			})
		}
		return
	}
	target := cli.NormalizeReleaseTag(latest)
	if target == "" {
		target = latest
	}
	d.updateObservation.Transition(func(obs *protocol.DaemonUpdateObservation) {
		obs.TargetVersion = target
		obs.ObservedAt = time.Now().UTC().Format(time.RFC3339Nano)
	})
	if d.logger != nil {
		d.logger.Info("auto-update detection: newer release found", "current", current, "target", target)
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

// tryAutoUpdate remains a private compatibility seam for older in-process
// callers/tests. Detection is handled by autoUpdateLoop; this method performs
// no mutation.
func (d *Daemon) tryAutoUpdate(ctx context.Context) {
	_ = ctx
}

// fetchReleaseManifest returns the current release manifest (the "/manifest.json"
// feed, with /latest.json legacy fallback) from the canonical release feed URL
// resolved by releaseManifestBaseURL(). It is kept small and dependency-light on
// purpose so the detection loop never touches the server API client.
func fetchReleaseManifest(ctx context.Context) (*cli.ReleaseManifest, error) {
	base := cli.ReleaseManifestBaseURL()
	manifestURLs := []string{base + "/manifest.json", base + "/latest.json", base + "/release.json"}
	var lastErr error
	for _, u := range manifestURLs {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			lastErr = err
			continue
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("release manifest %s: unexpected status %d", u, resp.StatusCode)
			continue
		}
		var manifest cli.ReleaseManifest
		if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
			resp.Body.Close()
			lastErr = err
			continue
		}
		resp.Body.Close()
		return &manifest, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("release manifest: no manifest found at %s", base)
	}
	return nil, lastErr
}
