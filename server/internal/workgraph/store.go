package workgraph

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Store synchronizes issue data into the Wendy work graph.
type Store struct {
	pool         *pgxpool.Pool
	queries      *db.Queries
	OnNodesReady func(context.Context, string, []string)
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:    pool,
		queries: db.New(pool),
	}
}
