package computer

import (
	"errors"
	"testing"
)

func TestValidateCreateRequiresAllIds(t *testing.T) {
	cases := []BindingRequest{
		{},
		{ActorUserID: "u" },
		{ActorUserID: "u", TargetComputerID: "c"},
		{ActorUserID: "u", TargetWorkspaceID: "ws"},
		{TargetComputerID: "c", TargetWorkspaceID: "ws"},
	}
	for _, req := range cases {
		if _, err := ValidateCreate(req, nil); err == nil {
			t.Fatalf("ValidateCreate(%+v) should error on missing ids", req)
		}
	}
}

func TestValidateCreateFreshAndRepair(t *testing.T) {
	req := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws"}

	if kind, err := ValidateCreate(req, nil); err != nil || kind != ValidationKindCreate {
		t.Fatalf("fresh create = kind %v, err %v; want Create", kind, err)
	}

	existing := []WorkspaceBinding{{WorkspaceID: "ws", Active: true}}
	if kind, err := ValidateCreate(req, existing); err != nil || kind != ValidationKindRepair {
		t.Fatalf("repeat create = kind %v, err %v; want Repair (idempotent)", kind, err)
	}
}

func TestValidateCreateRevokedIsCreateNotRepair(t *testing.T) {
	req := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws"}
	existing := []WorkspaceBinding{{WorkspaceID: "ws", Active: false}}
	if kind, err := ValidateCreate(req, existing); err != nil || kind != ValidationKindCreate {
		t.Fatalf("revoked binding = kind %v, err %v; want Create (no quiet repair)", kind, err)
	}
}

func TestValidateRemoveFailsClosed(t *testing.T) {
	empty := BindingRequest{ActorUserID: "", TargetComputerID: "c", TargetWorkspaceID: "ws"}
	if err := ValidateRemove(empty, nil); !errors.Is(err, ErrBindingUnauthorized) {
		t.Fatalf("remove with empty actor should fail closed, got %v", err)
	}

	noActor := BindingRequest{ActorUserID: "", TargetComputerID: "c", TargetWorkspaceID: "ws"}
	if err := ValidateRemove(noActor, nil); err == nil {
		t.Fatal("remove without actor should error")
	}

	absent := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws"}
	if err := ValidateRemove(absent, nil); !errors.Is(err, ErrBindingUnauthorized) {
		t.Fatalf("removing a non-existent binding should fail closed, got %v", err)
	}
}

func TestValidateRemoveAllowsExistingOwnBinding(t *testing.T) {
	req := BindingRequest{ActorUserID: "u", TargetComputerID: "c", TargetWorkspaceID: "ws"}
	existing := []WorkspaceBinding{{WorkspaceID: "ws", Active: true}}
	if err := ValidateRemove(req, existing); err != nil {
		t.Fatalf("removing an existing binding should succeed, got %v", err)
	}
}
