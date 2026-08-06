package handler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
)

const DefaultRuntimeReleaseCacheTTL = 5 * time.Minute

// RuntimeReleaseSource provides the latest public CLI release that the server
// can safely offer to local standalone daemons. It is intentionally separate
// from UpdateStore: UpdateStore tracks a specific requested update, while this
// is the read-only release channel used to decide whether an idle runtime is
// behind.
type RuntimeReleaseSource interface {
	Latest(ctx context.Context) (*RuntimeRelease, error)
}

type RuntimeRelease struct {
	TagName string
}

type CachedRuntimeReleaseSource struct {
	mu     sync.Mutex
	ttl    time.Duration
	now    func() time.Time
	fetch  func() (*cli.ReleaseManifest, error)
	cached *RuntimeRelease
	err    error
	at     time.Time
}

func NewCachedRuntimeReleaseSource(ttl time.Duration) *CachedRuntimeReleaseSource {
	if ttl <= 0 {
		ttl = DefaultRuntimeReleaseCacheTTL
	}
	return &CachedRuntimeReleaseSource{
		ttl:   ttl,
		now:   time.Now,
		fetch: cli.FetchLatestRelease,
	}
}

func (s *CachedRuntimeReleaseSource) Latest(ctx context.Context) (*RuntimeRelease, error) {
	if s == nil {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !s.at.IsZero() && now.Sub(s.at) < s.ttl {
		return s.cached, s.err
	}

	release, err := s.fetch()
	if err == nil {
		s.cached, err = runtimeReleaseFromManifest(release)
	} else {
		s.cached = nil
	}
	s.err = err
	s.at = now
	return s.cached, s.err
}

func runtimeReleaseFromManifest(manifest *cli.ReleaseManifest) (*RuntimeRelease, error) {
	if manifest == nil {
		return nil, fmt.Errorf("latest release manifest is empty")
	}
	tag := strings.TrimSpace(manifest.TagName)
	if tag == "" {
		return nil, fmt.Errorf("latest release tag is empty")
	}
	if !cli.IsReleaseVersion(tag) {
		return nil, fmt.Errorf("latest release tag %q is not a stable CLI release", tag)
	}
	if len(manifest.Platforms) == 0 {
		return nil, fmt.Errorf("latest release %s has no platform assets", tag)
	}
	return &RuntimeRelease{TagName: tag}, nil
}
