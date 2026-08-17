package memorygraph

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// legacyMemoryBlockSep separates entries in a legacy MEMORY.md written by
// the memorycuration promote pipeline (engine.go promoteEntry).
const legacyMemoryBlockSep = "\n§\n"

// MigrateLegacyMEMORY imports a legacy MEMORY.md into the graph as level-0
// statement nodes in the current version (design §8 item 7: 存量 MEMORY.md
// 是否导入图为初始叶子). Each "§"-separated block of the memorycuration
// promote format —
//
//	[type:<type>]
//	[source:<date>]
//	[evidence:<a,b>]
//	- <body>
//
// — becomes one node with epistemic status supported and tag
// "legacy-import". The block's [source:] date, when parseable, becomes
// ObservedAt. createdBy records the actor on the imported nodes; pass
// CreatorMigration ("migration") for the standard import. Returns the number
// of nodes created. A missing file is not an error: nothing to migrate.
func MigrateLegacyMEMORY(mdPath string, store *Store, createdBy string) (int, error) {
	if createdBy == "" {
		createdBy = CreatorMigration
	}
	b, err := os.ReadFile(mdPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("migrate legacy memory: read %s: %w", mdPath, err)
	}
	current, err := store.CurrentVersion()
	if err != nil {
		return 0, fmt.Errorf("migrate legacy memory: current version: %w", err)
	}

	created := 0
	// parts[0] is the file header before the first separator (title plus any
	// preamble), never an entry — same convention as memorycuration's
	// sweepExpiredState.
	for _, block := range strings.Split(string(b), legacyMemoryBlockSep)[1:] {
		body, observedAt := parseLegacyMemoryBlock(block)
		if body == "" {
			continue
		}
		node := &Node{
			NodeID:         uuid.NewString(),
			Level:          0,
			Epistemic:      StatusSupported,
			ObservedAt:     observedAt,
			TemporalStatus: TemporalUnknown,
			Tags:           []string{"legacy-import"},
			CreatedBy:      createdBy,
			CreatedVersion: current,
			UpdatedVersion: current,
			Body:           body,
		}
		if err := store.SaveNode(current, node); err != nil {
			return created, fmt.Errorf("migrate legacy memory: save imported node: %w", err)
		}
		created++
	}
	if created > 0 {
		// Keep the version manifest's node count truthful for audits.
		if m, err := store.LoadManifest(current); err == nil {
			m.NodeCount += created
			if err := store.SaveManifest(current, m); err != nil {
				return created, fmt.Errorf("migrate legacy memory: update manifest v%d: %w", current, err)
			}
		}
	}
	return created, nil
}

// parseLegacyMemoryBlock extracts the statement body and the [source:] date
// from one §-separated MEMORY.md block. Bracket metadata lines
// ([type:..], [source:..], [evidence:..], [expires_at:..]) and heading lines
// (the file header before the first separator) are dropped; the remaining
// text, with leading "- " bullets stripped, is the body.
func parseLegacyMemoryBlock(block string) (body string, observedAt time.Time) {
	var lines []string
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "§" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			if strings.HasPrefix(line, "[source:") {
				if d, err := time.Parse("2006-01-02", strings.TrimSuffix(strings.TrimPrefix(line, "[source:"), "]")); err == nil {
					observedAt = d.UTC()
				}
			}
			continue
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(line, "-")))
	}
	return strings.Join(lines, "\n"), observedAt
}
