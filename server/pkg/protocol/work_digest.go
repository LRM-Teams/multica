package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxWorkDigestCommitsPerRepo = 200
	maxWorkDigestDirtyPerRepo   = 200
	maxWorkDigestSubjectRunes   = 200
	maxWorkDigestPathBytes      = 512
)

const (
	WorkDigestDirtyAdded     = "added"
	WorkDigestDirtyModified  = "modified"
	WorkDigestDirtyDeleted   = "deleted"
	WorkDigestDirtyUntracked = "untracked"
)

// WorkDigest is the bounded Machine Work Journal summary for one time window.
// It carries git metadata and dirty paths only — never file bodies, diffs,
// patches, or Daily text. Callers must use ParseWorkDigest so unknown
// file-body fields fail closed.
type WorkDigest struct {
	ComputerID string           `json:"computer_id"`
	Window     WorkDigestWindow `json:"window"`
	Disabled   bool             `json:"disabled,omitempty"`
	Repos      []WorkDigestRepo `json:"repos"`
}

type WorkDigestWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type WorkDigestRepo struct {
	Root    string                `json:"root"`
	Remotes []string              `json:"remotes,omitempty"`
	Commits []WorkDigestCommit    `json:"commits"`
	Dirty   []WorkDigestDirtyPath `json:"dirty"`
}

type WorkDigestCommit struct {
	Hash       string    `json:"hash"`
	At         time.Time `json:"at"`
	Author     string    `json:"author"`
	Subject    string    `json:"subject"`
	FileCount  int       `json:"file_count"`
	Insertions int       `json:"insertions"`
	Deletions  int       `json:"deletions"`
}

type WorkDigestDirtyPath struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

func ParseWorkDigest(raw []byte) (WorkDigest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var digest WorkDigest
	if err := decoder.Decode(&digest); err != nil {
		return WorkDigest{}, fmt.Errorf("work digest: %w", err)
	}
	if err := digest.Validate(); err != nil {
		return WorkDigest{}, err
	}
	return digest, nil
}

func (d WorkDigest) Validate() error {
	if strings.TrimSpace(d.ComputerID) == "" {
		return fmt.Errorf("work digest computer_id is required")
	}
	if !d.Window.End.After(d.Window.Start) {
		return fmt.Errorf("work digest window end must be after start")
	}
	for i, repo := range d.Repos {
		if err := repo.validate(); err != nil {
			return fmt.Errorf("work digest repos[%d]: %w", i, err)
		}
	}
	return nil
}

func (r WorkDigestRepo) validate() error {
	if strings.TrimSpace(r.Root) == "" {
		return fmt.Errorf("root is required")
	}
	if len(r.Commits) > maxWorkDigestCommitsPerRepo {
		return fmt.Errorf("commits exceed %d", maxWorkDigestCommitsPerRepo)
	}
	if len(r.Dirty) > maxWorkDigestDirtyPerRepo {
		return fmt.Errorf("dirty paths exceed %d", maxWorkDigestDirtyPerRepo)
	}
	for i, commit := range r.Commits {
		if err := commit.validate(); err != nil {
			return fmt.Errorf("commits[%d]: %w", i, err)
		}
	}
	for i, dirty := range r.Dirty {
		if err := dirty.validate(); err != nil {
			return fmt.Errorf("dirty[%d]: %w", i, err)
		}
	}
	return nil
}

func (c WorkDigestCommit) validate() error {
	if strings.TrimSpace(c.Hash) == "" {
		return fmt.Errorf("hash is required")
	}
	if utf8.RuneCountInString(c.Subject) > maxWorkDigestSubjectRunes {
		return fmt.Errorf("subject exceeds %d runes", maxWorkDigestSubjectRunes)
	}
	if c.FileCount < 0 || c.Insertions < 0 || c.Deletions < 0 {
		return fmt.Errorf("file_count, insertions, and deletions must be >= 0")
	}
	return nil
}

func (d WorkDigestDirtyPath) validate() error {
	path := strings.TrimSpace(d.Path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if len(path) > maxWorkDigestPathBytes {
		return fmt.Errorf("path exceeds %d bytes", maxWorkDigestPathBytes)
	}
	switch d.Status {
	case WorkDigestDirtyAdded, WorkDigestDirtyModified, WorkDigestDirtyDeleted, WorkDigestDirtyUntracked:
		return nil
	default:
		return fmt.Errorf("invalid dirty status %q", d.Status)
	}
}
