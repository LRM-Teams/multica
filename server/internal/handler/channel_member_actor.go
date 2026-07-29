package handler

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

const (
	channelMemberActorUser   = "user"
	channelMemberActorAgent  = "agent"
	channelMemberActorSystem = "system"
)

type channelMemberActor struct {
	Type string
	ID   pgtype.UUID
}

func channelMemberUserActor(id pgtype.UUID) channelMemberActor {
	return channelMemberActor{Type: channelMemberActorUser, ID: id}
}

func channelMemberAgentActor(id pgtype.UUID) channelMemberActor {
	return channelMemberActor{Type: channelMemberActorAgent, ID: id}
}

func channelMemberSystemActor() channelMemberActor {
	return channelMemberActor{Type: channelMemberActorSystem}
}

func validChannelMemberActorID(id pgtype.UUID) bool {
	return id.Valid && id.Bytes != [16]byte{}
}

// validateChannelMemberActorWithExec is the shared application boundary for
// every channel-membership writer. Migration 245 installs the matching DB
// trigger so open-coded or future writes cannot bypass the same-workspace
// existence invariant.
func validateChannelMemberActorWithExec(
	ctx context.Context,
	exec dbExecutor,
	workspaceID string,
	actor channelMemberActor,
) error {
	if actor.Type == channelMemberActorSystem {
		if !actor.ID.Valid {
			return nil
		}
	} else if !validChannelMemberActorID(actor.ID) {
		return fmt.Errorf(
			"channel member actor %s/%s is not an existing same-workspace actor",
			actor.Type,
			uuidToString(actor.ID),
		)
	}

	var exists bool
	var err error
	switch actor.Type {
	case channelMemberActorUser:
		err = exec.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM member
			  WHERE workspace_id = $1 AND user_id = $2
			)`,
			parseUUID(workspaceID), actor.ID).Scan(&exists)
	case channelMemberActorAgent:
		err = exec.QueryRow(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM agent
			  WHERE workspace_id = $1 AND id = $2
			)`,
			parseUUID(workspaceID), actor.ID).Scan(&exists)
	default:
		return fmt.Errorf("unsupported channel member actor type %q", actor.Type)
	}
	if err != nil {
		return fmt.Errorf("validate channel member actor: %w", err)
	}
	if !exists {
		return fmt.Errorf(
			"channel member actor %s/%s is not an existing same-workspace actor",
			actor.Type,
			uuidToString(actor.ID),
		)
	}
	return nil
}
