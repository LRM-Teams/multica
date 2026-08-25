package computer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workJournalSetting struct {
	Enabled bool `json:"enabled"`
}

func workJournalSettingPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "work-journal.json")
}

// SetWorkJournalEnabled is the Computer-local Journal switch. The file under
// the resident root is authoritative; missing files mean disabled.
func (computerCore *ComputerCore) SetWorkJournalEnabled(enabled bool) error {
	if computerCore == nil {
		return errors.New("ComputerCore is unavailable")
	}
	computerCore.workJournalMu.Lock()
	defer computerCore.workJournalMu.Unlock()
	computerCore.workJournalEnabled = enabled
	return writeWorkJournalSetting(computerCore.workJournalRoot, enabled)
}

// WorkJournalEnabled reports the in-memory Journal switch (default off).
func (computerCore *ComputerCore) WorkJournalEnabled() bool {
	if computerCore == nil {
		return false
	}
	computerCore.workJournalMu.Lock()
	defer computerCore.workJournalMu.Unlock()
	return computerCore.workJournalEnabled
}

func (computerCore *ComputerCore) loadWorkJournalSetting() {
	if computerCore == nil {
		return
	}
	enabled, err := readWorkJournalSetting(computerCore.workJournalRoot)
	if err != nil {
		return
	}
	computerCore.workJournalMu.Lock()
	computerCore.workJournalEnabled = enabled
	computerCore.workJournalMu.Unlock()
}

func writeWorkJournalSetting(root string, enabled bool) error {
	path := workJournalSettingPath(root)
	if path == "" {
		return nil
	}
	data, err := json.Marshal(workJournalSetting{Enabled: enabled})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".work-journal-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func readWorkJournalSetting(root string) (bool, error) {
	path := workJournalSettingPath(root)
	if path == "" {
		return false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var setting workJournalSetting
	if err := json.Unmarshal(raw, &setting); err != nil {
		return false, err
	}
	return setting.Enabled, nil
}

// HarvestWorkDigest collects the Owner's Work Digest for one window. Journal
// defaults off; a disabled harvest is an empty repos list, not an error.
func (computerCore *ComputerCore) HarvestWorkDigest(ctx context.Context, command protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error) {
	if computerCore == nil {
		return protocol.WorkDigest{}, errors.New("ComputerCore is unavailable")
	}
	if err := command.Validate(); err != nil {
		return protocol.WorkDigest{}, err
	}
	computerCore.workJournalMu.Lock()
	enabled := computerCore.workJournalEnabled
	home := strings.TrimSpace(computerCore.workJournalHome)
	computerID := strings.TrimSpace(computerCore.processIdentity.ComputerID)
	computerCore.workJournalMu.Unlock()
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil && enabled {
			return protocol.WorkDigest{}, err
		}
		home = userHome
	}
	if computerID == "" {
		computerID = "computer"
	}
	return HarvestWorkJournal(ctx, WorkJournalHarvestRequest{
		ComputerID: computerID,
		Home:       home,
		Window:     command.Window(),
		Enabled:    enabled,
	})
}
