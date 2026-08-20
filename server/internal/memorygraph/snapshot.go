package memorygraph

import (
	"fmt"
	"os"
)

// GraphSnapshot is one immutable view of a stored version: the manifest,
// the version's graph (nodes + edges), and the source-layer records visible
// at the manifest watermark (spec §15).
type GraphSnapshot struct {
	Manifest    *Manifest
	Graph       *Graph
	SourceNodes []*Node
	SourceEdges []*Edge
}

// OpenSnapshot loads version under a single store lock. A missing version
// fails closed; the returned structures are freshly loaded copies.
func (s *Store) OpenSnapshot(version int) (*GraphSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.VersionDir(version)); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("version v%d does not exist", version)
		}
		return nil, fmt.Errorf("stat version v%d: %w", version, err)
	}
	m, err := s.loadManifestLocked(version)
	if err != nil {
		return nil, err
	}
	g, err := s.loadGraphLocked(version)
	if err != nil {
		return nil, err
	}
	srcNodes, srcEdges, err := s.loadSourcesLocked(m.SourceWatermark)
	if err != nil {
		return nil, err
	}
	return &GraphSnapshot{
		Manifest:    m,
		Graph:       g,
		SourceNodes: srcNodes,
		SourceEdges: srcEdges,
	}, nil
}

func (s *Store) loadGraphLocked(v int) (*Graph, error) {
	nodes, err := s.loadNodesLocked(v)
	if err != nil {
		return nil, err
	}
	hier, rel, err := s.loadEdgesLocked(v)
	if err != nil {
		return nil, err
	}
	g := newGraph()
	for _, n := range nodes {
		g.nodes[n.NodeID] = n
	}
	g.hier = hier
	g.rel = rel
	g.rebuild()
	return g, nil
}
