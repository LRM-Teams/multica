package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

// Indirections over the real release / version helpers so tests can run the
// auto-update loop deterministically without reaching out to GitHub or
// shelling out to brew/curl. Mirrors the pattern used at the top of daemon.go
// for `isBrewInstall` / `getBrewPrefix` / `matchKnownBrewPrefix`.
var (
	fetchLatestRelease   = cli.FetchLatestReleaseWithOverride
	fetchReleaseByTagVar = cli.FetchReleaseByTagWithOverride
	isReleaseVersion     = cli.IsReleaseVersion
	isNewerVersion       = cli.IsNewerVersion
)

// autoUpdateInitialDelay is how long the loop waits after Run() returns before
// performing its first version check. The daemon has plenty to do at startup
// (auth, register, sync workspaces, kick off heartbeats); we don't want to add
// an outbound HTTPS call to GitHub on top of that. The delay is also short
// enough that a brand-new install with an available update still self-updates
// within a couple of minutes rather than after the full check interval.
var autoUpdateInitialDelay = 2 * time.Minute

// autoUpdateLoop periodically polls GitHub for a newer CLI release and, when
// one is available and the daemon is idle, runs the same brew-or-download
// upgrade path as the server-triggered update. On success it triggers a
// graceful restart into the new binary.
//
// Disabled when:
//   - the operator opted out via --no-auto-update / MULTICA_DAEMON_AUTO_UPDATE=false;
//   - the daemon points at a self-hosted server (default-off — set
//     MULTICA_DAEMON_AUTO_UPDATE=true to opt back in);
//   - the daemon was spawned by Desktop (the Electron app owns the binary);
//   - the running version doesn't look like a tagged release (dev builds).
//
// Each tick is silent on the happy path of "already on latest" so the log
// stays uncluttered for users who run the daemon for weeks at a time.
func (d *Daemon) autoUpdateLoop(ctx context.Context) {
	if !d.cfg.AutoUpdateEnabled {
		d.logger.Info("auto-update: disabled")
		return
	}
	if d.cfg.LaunchedBy == "desktop" {
		// Desktop ships and replaces the CLI binary itself; self-update would
		// be clobbered on the next launch. Stay quiet but don't run.
		d.logger.Info("auto-update: skipped (managed by Desktop)")
		return
	}
	if !isReleaseVersion(d.cfg.CLIVersion) {
		// Source builds (`make daemon`) and ad-hoc builds report a
		// `git describe`-style version; auto-upgrading them to a public
		// release would silently downgrade the dev work checked out on the
		// machine. Skip and let the developer drive their own version.
		d.logger.Info("auto-update: skipped (not a release build)", "version", d.cfg.CLIVersion)
		return
	}
	if d.cfg.PinnedVersion != "" {
		// Operator explicitly pinned this machine to a specific version via
		// MULTICA_PINNED_VERSION.
		if d.cfg.CLIVersion == d.cfg.PinnedVersion {
			// Already running the pinned version — stay put, no upgrades.
			d.logger.Info("auto-update: skipped (version pinned)",
				"pinned", d.cfg.PinnedVersion,
				"current", d.cfg.CLIVersion)
			if d.updateObservation != nil {
				d.beginUpdateObservation("auto", "waiting", "")
				d.finishUpdateObservation("waiting", "pinned", d.cfg.PinnedVersion, "",
					fmt.Sprintf("This machine is pinned to version %s via MULTICA_PINNED_VERSION and will not auto-upgrade.", d.cfg.PinnedVersion))
			}
			return
		}
		// Pinned version differs from current — attempt to install it once,
		// then stop. The install path is the same as tryAutoUpdate but
		// targets the pinned version instead of fetching latest.
		d.logger.Info("auto-update: pinned version differs from current, installing pinned version",
			"pinned", d.cfg.PinnedVersion,
			"current", d.cfg.CLIVersion)
		// Fall through to the normal loop; tryAutoUpdate will be called with
		// a pin-aware path that targets the pinned version.
	}

	interval := d.cfg.AutoUpdateCheckInterval
	if interval <= 0 {
		interval = DefaultAutoUpdateCheckInterval
	}
	d.logger.Info("auto-update: started", "interval", interval, "current", d.cfg.CLIVersion)

	if err := sleepWithContext(ctx, autoUpdateInitialDelay); err != nil {
		return
	}
	d.tryAutoUpdate(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.tryAutoUpdate(ctx)
		}
	}
}

// tryAutoUpdate runs one check-and-maybe-upgrade cycle. Bails early on any of:
// already updating (server-triggered upgrade in flight), active tasks (defer
// to next tick — we never interrupt running agents), version fetch failure,
// or no newer release. The function never returns an error: a check that
// fails today will be retried at the next tick, and we don't want a transient
// network blip to escalate to a process-level shutdown.
func (d *Daemon) tryAutoUpdate(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}

	// Defense-in-depth: even if tryAutoUpdate is called directly (e.g. by a
	// server-triggered update), respect the pin. The loop-level guard in
	// autoUpdateLoop prevents the periodic ticker from reaching here, but a
	// manual or server-initiated update request should also be blocked.
	if d.cfg.PinnedVersion != "" && d.cfg.CLIVersion == d.cfg.PinnedVersion {
		// Already on the pinned version — do not upgrade beyond it.
		d.logger.Info("auto-update: skip — version pinned",
			"pinned", d.cfg.PinnedVersion,
			"current", d.cfg.CLIVersion)
		return
	}
	// If pinned but not yet on the pinned version, fall through and let the
	// normal upgrade path install it. The release fetch below will be
	// overridden to target the pinned version instead of latest.

	// Own the whole attempt before publishing any observation, including a
	// busy result from the cheap pre-fetch check below. Checking the flag and
	// publishing first allowed a concurrent server update to finish and then
	// be overwritten by this caller's stale result.
	if !d.updating.CompareAndSwap(false, true) {
		d.logger.Debug("auto-update: skip — update already in progress")
		return
	}
	released := false
	defer func() {
		if !released {
			d.updating.Store(false)
		}
	}()

	// Cheap pre-fetch idle check: the release-metadata fetch below makes an
	// HTTPS call to GitHub, and there is no point paying that cost (or the
	// rate-limit budget) when we already know we are going to defer. A task
	// that starts between this load and the barrier check below is caught
	// by the strict re-check under claimMu inside trySetClaimBarrier.
	if running := d.activeTasks.Load(); running > 0 {
		d.logger.Debug("auto-update: skip — tasks running", "active", running)
		if d.beginUpdateObservation("auto", "waiting", "") {
			d.finishUpdateObservation("waiting", "busy", "", "", "")
		}
		return
	}

	if !d.beginUpdateObservation("auto", "checking", "") {
		return
	}

	// When pinned to a specific version that isn't the current version,
	// fetch that specific release instead of "latest". This installs the
	// pinned version exactly once; after restart the current version will
	// match the pin and the loop exits early.
	var release *cli.ReleaseManifest
	var err error
	if d.cfg.PinnedVersion != "" && d.cfg.CLIVersion != d.cfg.PinnedVersion {
		release, err = fetchReleaseByTagVar(d.cfg.PinnedVersion, d.releaseManifestBaseURLOverride())
		if err != nil {
			d.logger.Warn("auto-update: fetch pinned release failed — will retry",
				"pinned", d.cfg.PinnedVersion, "error", err)
			d.finishUpdateObservation("waiting", "fetch_failed", d.cfg.PinnedVersion,
				"release_fetch_failed",
				fmt.Sprintf("Unable to fetch the pinned release %s.", d.cfg.PinnedVersion))
			return
		}
	} else {
		release, err = fetchLatestRelease(d.releaseManifestBaseURLOverride())
		if err != nil {
			d.logger.Warn("auto-update: fetch latest release failed — will retry", "error", err)
			d.finishUpdateObservation("waiting", "fetch_failed", "", "release_fetch_failed", "Unable to fetch the latest release.")
			return
		}
	}
	if release == nil || release.TagName == "" {
		d.finishUpdateObservation("waiting", "up_to_date", "", "", "")
		return
	}
	if d.cfg.PinnedVersion == "" && !isNewerVersion(release.TagName, d.cfg.CLIVersion) {
		d.finishUpdateObservation("waiting", "up_to_date", release.TagName, "", "")
		return
	}

	// Strict barrier: between the cheap pre-fetch idle check and now the
	// release fetch took anywhere from tens of milliseconds (typical) to
	// seconds (slow link, GitHub hiccup), plenty of time for a poller to
	// claim a fresh task. trySetClaimBarrier checks claimsInFlight +
	// activeTasks under claimMu and only flips pauseClaims to true if both
	// are zero, so once it returns true we can run the upgrade knowing that
	// no in-flight task will be cancelled by triggerRestart.
	if !d.trySetClaimBarrier() {
		d.logger.Info("auto-update: deferring — task or claim in flight at barrier check")
		d.finishUpdateObservation("waiting", "busy", release.TagName, "", "")
		return
	}
	barrierReleased := false
	defer func() {
		if !barrierReleased {
			d.releaseClaimBarrier()
		}
	}()

	d.logger.Info("auto-update: newer release available, upgrading",
		"current", d.cfg.CLIVersion, "target", release.TagName)
	if !d.beginUpdateObservation("auto", "updating", release.TagName) {
		return
	}

	output, err := d.runUpdateFn(release.TagName)
	if err != nil {
		d.logger.Warn("auto-update: upgrade failed — will retry", "error", err, "output", output)
		d.finishUpdateObservation("waiting", "update_failed", release.TagName, "download_update_failed", "The release download update failed.")
		return
	}
	verifiedVersion, err := d.verifyUpdatedBinaryVersion(release.TagName, output)
	if err != nil {
		d.logger.Warn("auto-update: upgrade verification failed — will retry", "error", err, "output", output)
		d.finishUpdateObservation("waiting", "verification_failed", release.TagName, "updated_binary_verification_failed", "The updated CLI version could not be verified.")
		return
	}

	d.logger.Info("auto-update: staged; activating and restarting", "target", release.TagName, "output", output, "verified_version", verifiedVersion)
	// Stage-only path: CAS Active to staged tag then re-exec staged binary.
	// Leave updating + barrier held through process exit (same as before).
	if !d.finishUpdateObservation("restart_pending", "update_succeeded", release.TagName, "", "") {
		return
	}
	activate := d.activateStagedFn
	if activate == nil {
		activate = d.commitStagedActivation
	}
	path, err := activate(ctx, "auto-"+release.TagName, output)
	if err != nil {
		d.logger.Warn("auto-update: activate CAS failed — will retry", "error", err)
		d.finishUpdateObservation("waiting", "update_failed", release.TagName, "activation_cas_failed", "Staged release could not be activated.")
		return
	}
	if path != "" {
		d.restartBinary = path
	}
	released = true
	barrierReleased = true
	d.triggerRestart()
}
