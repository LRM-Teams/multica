package workgraph

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Epoch struct {
	ID             string          `json:"id"`
	GoalID         string          `json:"goal_id"`
	GraphID        string          `json:"graph_id"`
	Number         int64           `json:"epoch_number"`
	Status         string          `json:"status"`
	LeaseToken     string          `json:"lease_token,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	Contract       json.RawMessage `json:"contract"`
	Budget         json.RawMessage `json:"budget"`
	Evaluation     json.RawMessage `json:"evaluation"`
	Decision       *string         `json:"decision,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CommittedAt    *time.Time      `json:"committed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type StartEpochInput struct {
	WorkspaceID  string          `json:"-"`
	GraphID      string          `json:"-"`
	ActorAgentID string          `json:"-"`
	Contract     json.RawMessage `json:"contract"`
	Budget       json.RawMessage `json:"budget"`
}

type FinishEpochInput struct {
	WorkspaceID  string          `json:"-"`
	GraphID      string          `json:"-"`
	EpochID      string          `json:"-"`
	ActorAgentID string          `json:"-"`
	Evaluation   json.RawMessage `json:"evaluation"`
	Decision     string          `json:"decision"`
	LeaseToken   string          `json:"lease_token"`
}

var epochDecisions = map[string]bool{
	"CONTINUE": true, "WAIT": true, "ASK_HUMAN": true,
	"RETRY_OPERATION": true, "REPAIR_CONTRACT": true, "REPLAN_NEW_AXIS": true,
	"STOP_CONVERGED": true, "STOP_NO_GAIN": true, "STOP_BUDGET": true,
}

func validJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil
	}
	return raw
}

func scanEpoch(row interface{ Scan(...any) error }) (Epoch, error) {
	var out Epoch
	var contract, budget, evaluation []byte
	err := row.Scan(&out.ID, &out.GoalID, &out.GraphID, &out.Number, &out.Status, &contract, &budget, &evaluation, &out.Decision, &out.LeaseToken, &out.LeaseExpiresAt, &out.StartedAt, &out.CommittedAt, &out.CreatedAt)
	out.Contract, out.Budget, out.Evaluation = contract, budget, evaluation
	return out, err
}

func (s *Store) StartEpoch(ctx context.Context, in StartEpochInput) (Epoch, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	actor, err := uuid.Parse(in.ActorAgentID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	contract, budget := validJSONObject(in.Contract), validJSONObject(in.Budget)
	if contract == nil || budget == nil {
		return Epoch{}, ErrInvalidGraph
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Epoch{}, err
	}
	defer tx.Rollback(ctx)
	var goal uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT anchor_id FROM work_graph WHERE workspace_id=$1 AND id=$2 AND anchor_kind='channel_goal' AND status='active' FOR UPDATE`, w, g).Scan(&goal); err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	var number int64
	// Recover abandoned epochs before enforcing the one-live-epoch invariant.
	if _, err = tx.Exec(ctx, `UPDATE goal_execution_epoch SET status='cancelled',lease_owner=NULL,lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE workspace_id=$1 AND goal_id=$2 AND status IN('running','evaluating','waiting') AND lease_expires_at < now()`, w, goal); err != nil {
		return Epoch{}, err
	}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(epoch_number),0)+1 FROM goal_execution_epoch WHERE workspace_id=$1 AND goal_id=$2`, w, goal).Scan(&number); err != nil {
		return Epoch{}, err
	}
	out, err := scanEpoch(tx.QueryRow(ctx, `INSERT INTO goal_execution_epoch(workspace_id,goal_id,graph_id,epoch_number,status,contract,budget,lease_owner,lease_token,lease_expires_at,started_at) VALUES($1,$2,$3,$4,'running',$5,$6,$7,gen_random_uuid(),now()+interval '15 minutes',now()) RETURNING id::text,goal_id::text,graph_id::text,epoch_number,status,contract,budget,evaluation,decision,lease_token::text,lease_expires_at,started_at,committed_at,created_at`, w, goal, g, number, contract, budget, actor))
	if err != nil {
		return Epoch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Epoch{}, err
	}
	return out, nil
}

func (s *Store) FinishEpoch(ctx context.Context, in FinishEpochInput) (Epoch, error) {
	w, err := uuid.Parse(in.WorkspaceID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	g, err := uuid.Parse(in.GraphID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	e, err := uuid.Parse(in.EpochID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	actor, err := uuid.Parse(in.ActorAgentID)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	lease, err := uuid.Parse(in.LeaseToken)
	if err != nil {
		return Epoch{}, ErrInvalidGraph
	}
	in.Decision = strings.ToUpper(strings.TrimSpace(in.Decision))
	evaluation := validJSONObject(in.Evaluation)
	if !epochDecisions[in.Decision] || evaluation == nil {
		return Epoch{}, ErrInvalidGraph
	}
	status := "committed"
	if in.Decision == "WAIT" || in.Decision == "ASK_HUMAN" {
		status = "waiting"
	}
	if strings.HasPrefix(in.Decision, "STOP_") {
		status = "stopped"
	}
	out, err := scanEpoch(s.pool.QueryRow(ctx, `UPDATE goal_execution_epoch SET status=$6,evaluation=$7,decision=$8,committed_at=CASE WHEN $6 IN('committed','stopped') THEN now() ELSE committed_at END,lease_owner=CASE WHEN $6='waiting' THEN lease_owner ELSE NULL END,lease_token=CASE WHEN $6='waiting' THEN lease_token ELSE NULL END,lease_expires_at=CASE WHEN $6='waiting' THEN now()+interval '15 minutes' ELSE NULL END,updated_at=now() WHERE workspace_id=$1 AND graph_id=$2 AND id=$3 AND lease_owner=$4 AND lease_token=$5 AND lease_expires_at>now() AND status IN('running','evaluating','waiting') RETURNING id::text,goal_id::text,graph_id::text,epoch_number,status,contract,budget,evaluation,decision,COALESCE(lease_token::text,''),lease_expires_at,started_at,committed_at,created_at`, w, g, e, actor, lease, status, evaluation, in.Decision))
	if err != nil {
		return Epoch{}, ErrGraphConflict
	}
	return out, nil
}

func (s *Store) ListEpochs(ctx context.Context, workspaceID, graphID string) ([]Epoch, error) {
	w, err := uuid.Parse(workspaceID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	g, err := uuid.Parse(graphID)
	if err != nil {
		return nil, ErrInvalidGraph
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,goal_id::text,graph_id::text,epoch_number,status,contract,budget,evaluation,decision,COALESCE(lease_token::text,''),lease_expires_at,started_at,committed_at,created_at FROM goal_execution_epoch WHERE workspace_id=$1 AND graph_id=$2 ORDER BY epoch_number`, w, g)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Epoch{}
	for rows.Next() {
		epoch, scanErr := scanEpoch(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, epoch)
	}
	return out, rows.Err()
}
