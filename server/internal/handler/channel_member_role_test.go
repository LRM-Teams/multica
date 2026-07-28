package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestCreateChannelCreatorIsOwnerAndListReturnsRole locks Beckham v2 §4
// data-model slice 1: channel_member.role is exposed on list, creator is owner.
func TestCreateChannelCreatorIsOwnerAndListReturnsRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	creator := testUserID
	req := newRequestAs(creator, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-panel-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, creator)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", created.Code, created.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	listReq := newRequestAs(creator, http.MethodGet, "/api/channels/"+ch.ID+"/members", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, creator)
	listReq = withURLParam(listReq, "channelId", ch.ID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMembers(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChannelMembers = %d: %s", listRec.Code, listRec.Body.String())
	}
	var members []ChannelMemberResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members) == 0 {
		t.Fatal("expected at least creator in members")
	}
	// Creator must be first (owner sort) and role=owner.
	if members[0].MemberID != creator {
		t.Fatalf("first member = %s, want creator %s", members[0].MemberID, creator)
	}
	if members[0].Role != "owner" {
		t.Fatalf("creator role = %q, want owner", members[0].Role)
	}
	for _, m := range members {
		if m.Role == "" {
			t.Fatalf("member %+v missing role", m)
		}
	}
}
