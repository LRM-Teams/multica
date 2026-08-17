package computer

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// HarvestWorkDigest collects the Owner's Work Digest for one window. Journal
// defaults off; a disabled harvest is an empty repos list, not an error.
func (host *Host) HarvestWorkDigest(ctx context.Context, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if host == nil {
		return protocol.WorkDigest{}, errors.New("Computer Host is unavailable")
	}
	if err := command.Validate(); err != nil {
		return protocol.WorkDigest{}, err
	}
	home := strings.TrimSpace(host.workJournalHome)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil && host.workJournalEnabled {
			return protocol.WorkDigest{}, err
		}
		home = userHome
	}
	computerID := strings.TrimSpace(host.processIdentity.ComputerID)
	if computerID == "" {
		computerID = "computer"
	}
	return HarvestWorkJournal(ctx, WorkJournalHarvestRequest{
		ComputerID: computerID,
		Home:       home,
		Window:     command.Window(),
		Enabled:    host.workJournalEnabled,
	})
}
