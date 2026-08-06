package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/internal/auth"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type sandboxContextKey int

const (
	ctxKeySandboxNodeID sandboxContextKey = iota
	ctxKeySandboxNodeKey
	ctxKeySandboxJobID
)

func SandboxNodeIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeySandboxNodeID).(string)
	return id
}

func SandboxNodeKeyFromContext(ctx context.Context) string {
	key, _ := ctx.Value(ctxKeySandboxNodeKey).(string)
	return key
}

func SandboxJobIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeySandboxJobID).(string)
	return id
}

func SandboxNodeAuth(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(w, r)
			if !ok {
				return
			}
			if !strings.HasPrefix(token, "msn_") {
				writeError(w, http.StatusUnauthorized, "invalid sandbox node token")
				return
			}
			if queries == nil {
				writeError(w, http.StatusUnauthorized, "invalid sandbox node token")
				return
			}
			tok, err := queries.GetSandboxNodeTokenByHash(r.Context(), auth.HashToken(token))
			if err != nil {
				slog.Warn("sandbox_auth: invalid node token", "path", r.URL.Path, "error", err)
				writeError(w, http.StatusUnauthorized, "invalid sandbox node token")
				return
			}
			node, err := queries.GetSandboxNode(r.Context(), tok.NodeID)
			if err != nil {
				slog.Warn("sandbox_auth: token node missing", "path", r.URL.Path, "error", err)
				writeError(w, http.StatusUnauthorized, "invalid sandbox node token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeySandboxNodeID, uuidToString(node.ID))
			ctx = context.WithValue(ctx, ctxKeySandboxNodeKey, node.NodeKey)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func SandboxJobAuth(queries *db.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(w, r)
			if !ok {
				return
			}
			if !strings.HasPrefix(token, "mst_") {
				writeError(w, http.StatusUnauthorized, "invalid sandbox job token")
				return
			}
			if queries == nil {
				writeError(w, http.StatusUnauthorized, "invalid sandbox job token")
				return
			}
			job, err := queries.GetSandboxJobByTokenHash(r.Context(), auth.HashToken(token))
			if err != nil {
				slog.Warn("sandbox_auth: invalid job token", "path", r.URL.Path, "error", err)
				writeError(w, http.StatusUnauthorized, "invalid sandbox job token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeySandboxJobID, uuidToString(job.ID))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeError(w, http.StatusUnauthorized, "missing authorization header")
		return "", false
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == authHeader || strings.TrimSpace(token) == "" {
		writeError(w, http.StatusUnauthorized, "invalid authorization format")
		return "", false
	}
	return token, true
}
