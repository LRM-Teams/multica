package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type scopedFKTarget struct {
	local          string
	crossSession   string
	crossWorkspace string
}

type scopedFKMutation struct {
	name   string
	query  string
	target scopedFKTarget
}

// TestResearchArtifactDirectScopedFKMatrix closes the normalized half of
// Chapter D §15.3. Migration 326 owns the legacy domain relationship matrix;
// this test covers every Result/Manifest/Entry/Omission/Input/Supersession/
// Lifecycle/Policy-Mutation/Version endpoint introduced by Chapter D.
func TestResearchArtifactDirectScopedFKMatrix(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_direct_fk_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifactPassportLegacySchema); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifact329TestDDL); err != nil {
		t.Fatalf("extend attempt fixture: %v", err)
	}

	ids := directScopedFKIDs()
	seedDirectScopedFKDomain(t, ctx, conn.Conn(), ids)
	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up346, _ := readMigrationPair(t, "346_research_manifest_policy_grants")
	up361, down361 := readMigrationPair(t, "361_research_artifact_policy_mutation_scoped_fks")
	for _, migration := range []struct {
		name string
		sql  string
	}{{"318", up318}, {"346", up346}, {"361", up361}} {
		if _, err = conn.Exec(ctx, migration.sql); err != nil {
			t.Fatalf("apply migration %s: %v", migration.name, err)
		}
	}
	seedDirectScopedFKArtifacts(t, ctx, conn.Conn(), ids)

	for _, mutation := range directScopedFKMutations(ids) {
		t.Run(mutation.name+"/same-scope", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.local, true)
		})
		t.Run(mutation.name+"/cross-session", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.crossSession, false)
		})
		t.Run(mutation.name+"/cross-workspace", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.crossWorkspace, false)
		})
	}

	for _, mutation := range directScopedFKInsertMutations(ids) {
		t.Run(mutation.name+"/same-scope", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.local, true)
		})
		t.Run(mutation.name+"/cross-session", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.crossSession, false)
		})
		t.Run(mutation.name+"/cross-workspace", func(t *testing.T) {
			assertScopedFKMutation(t, ctx, conn.Conn(), mutation.query, mutation.target.crossWorkspace, false)
		})
	}

	if _, err = conn.Exec(ctx, down361); err != nil {
		t.Fatalf("apply 361 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up361); err != nil {
		t.Fatalf("reapply 361 up: %v", err)
	}
}

func directScopedFKIDs() map[string]scopedFKTarget {
	return map[string]scopedFKTarget{
		"workspace":         {local: "10000000-0000-4000-8000-000000000001", crossSession: "10000000-0000-4000-8000-000000000001", crossWorkspace: "10000000-0000-4000-8000-000000000002"},
		"session":           {local: "20000000-0000-4000-8000-000000000001", crossSession: "20000000-0000-4000-8000-000000000002", crossWorkspace: "20000000-0000-4000-8000-000000000003"},
		"contract":          {local: "30000000-0000-4000-8000-000000000001", crossSession: "30000000-0000-4000-8000-000000000002", crossWorkspace: "30000000-0000-4000-8000-000000000003"},
		"task":              {local: "31000000-0000-4000-8000-000000000001", crossSession: "31000000-0000-4000-8000-000000000002", crossWorkspace: "31000000-0000-4000-8000-000000000003"},
		"attempt":           {local: "32000000-0000-4000-8000-000000000001", crossSession: "32000000-0000-4000-8000-000000000002", crossWorkspace: "32000000-0000-4000-8000-000000000003"},
		"eval_attempt":      {local: "32100000-0000-4000-8000-000000000001", crossSession: "32100000-0000-4000-8000-000000000002", crossWorkspace: "32100000-0000-4000-8000-000000000003"},
		"decision":          {local: "33000000-0000-4000-8000-000000000001", crossSession: "33000000-0000-4000-8000-000000000002", crossWorkspace: "33000000-0000-4000-8000-000000000003"},
		"passport":          {local: "34000000-0000-4000-8000-000000000001", crossSession: "34000000-0000-4000-8000-000000000002", crossWorkspace: "34000000-0000-4000-8000-000000000003"},
		"passport2":         {local: "34100000-0000-4000-8000-000000000001", crossSession: "34100000-0000-4000-8000-000000000002", crossWorkspace: "34100000-0000-4000-8000-000000000003"},
		"version":           {local: "35000000-0000-4000-8000-000000000001", crossSession: "35000000-0000-4000-8000-000000000002", crossWorkspace: "35000000-0000-4000-8000-000000000003"},
		"version2":          {local: "35100000-0000-4000-8000-000000000001", crossSession: "35100000-0000-4000-8000-000000000002", crossWorkspace: "35100000-0000-4000-8000-000000000003"},
		"grant":             {local: "36000000-0000-4000-8000-000000000001", crossSession: "36000000-0000-4000-8000-000000000002", crossWorkspace: "36000000-0000-4000-8000-000000000003"},
		"eval_grant":        {local: "36100000-0000-4000-8000-000000000001", crossSession: "36100000-0000-4000-8000-000000000002", crossWorkspace: "36100000-0000-4000-8000-000000000003"},
		"eval_normal_grant": {local: "36200000-0000-4000-8000-000000000001", crossSession: "36200000-0000-4000-8000-000000000002", crossWorkspace: "36200000-0000-4000-8000-000000000003"},
		"manifest":          {local: "37000000-0000-4000-8000-000000000001", crossSession: "37000000-0000-4000-8000-000000000002", crossWorkspace: "37000000-0000-4000-8000-000000000003"},
		"eval_manifest":     {local: "37100000-0000-4000-8000-000000000001", crossSession: "37100000-0000-4000-8000-000000000002", crossWorkspace: "37100000-0000-4000-8000-000000000003"},
		"result":            {local: "38000000-0000-4000-8000-000000000001", crossSession: "38000000-0000-4000-8000-000000000002", crossWorkspace: "38000000-0000-4000-8000-000000000003"},
	}
}

func seedDirectScopedFKDomain(t *testing.T, ctx context.Context, conn *pgx.Conn, ids map[string]scopedFKTarget) {
	t.Helper()
	for _, scope := range []struct{ workspace, session string }{
		{ids["workspace"].local, ids["session"].local},
		{ids["workspace"].local, ids["session"].crossSession},
		{ids["workspace"].crossWorkspace, ids["session"].crossWorkspace},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO workspace(id) VALUES ($1::uuid) ON CONFLICT DO NOTHING`, scope.workspace); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_session(id,workspace_id,orchestrator_version) VALUES ($1::uuid,$2::uuid,'research-run-v5')`, scope.session, scope.workspace); err != nil {
			t.Fatal(err)
		}
	}
	for i, scope := range []struct{ workspace, session, contract, task, attempt, evalAttempt, decision string }{
		{ids["workspace"].local, ids["session"].local, ids["contract"].local, ids["task"].local, ids["attempt"].local, ids["eval_attempt"].local, ids["decision"].local},
		{ids["workspace"].local, ids["session"].crossSession, ids["contract"].crossSession, ids["task"].crossSession, ids["attempt"].crossSession, ids["eval_attempt"].crossSession, ids["decision"].crossSession},
		{ids["workspace"].crossWorkspace, ids["session"].crossWorkspace, ids["contract"].crossWorkspace, ids["task"].crossWorkspace, ids["attempt"].crossWorkspace, ids["eval_attempt"].crossWorkspace, ids["decision"].crossWorkspace},
	} {
		if _, err := conn.Exec(ctx, `INSERT INTO research_contract_revision(id,workspace_id,session_id,goal_version) VALUES ($1::uuid,$2::uuid,$3::uuid,1)`, scope.contract, scope.workspace, scope.session); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_task(id,workspace_id,session_id,client_key,goal_version,plan_version) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,1,1)`, scope.task, scope.workspace, scope.session, fmt.Sprintf("task-%d", i)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_task_attempt(id,workspace_id,session_id,task_id,status) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'succeeded')`, scope.attempt, scope.workspace, scope.session, scope.task); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_task_attempt(id,workspace_id,session_id,task_id,status) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'succeeded')`, scope.evalAttempt, scope.workspace, scope.session, scope.task); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_decision(id,workspace_id,session_id,decision_kind,goal_version,plan_version) VALUES ($1::uuid,$2::uuid,$3::uuid,'test',1,1)`, scope.decision, scope.workspace, scope.session); err != nil {
			t.Fatal(err)
		}
	}
}

func seedDirectScopedFKArtifacts(t *testing.T, ctx context.Context, conn *pgx.Conn, ids map[string]scopedFKTarget) {
	t.Helper()
	scopes := []struct {
		workspace, session, passport, passport2, version, version2, grant, evalGrant, evalNormalGrant, manifest, evalManifest, result, task, attempt, evalAttempt, contract, decision string
	}{
		{ids["workspace"].local, ids["session"].local, ids["passport"].local, ids["passport2"].local, ids["version"].local, ids["version2"].local, ids["grant"].local, ids["eval_grant"].local, ids["eval_normal_grant"].local, ids["manifest"].local, ids["eval_manifest"].local, ids["result"].local, ids["task"].local, ids["attempt"].local, ids["eval_attempt"].local, ids["contract"].local, ids["decision"].local},
		{ids["workspace"].local, ids["session"].crossSession, ids["passport"].crossSession, ids["passport2"].crossSession, ids["version"].crossSession, ids["version2"].crossSession, ids["grant"].crossSession, ids["eval_grant"].crossSession, ids["eval_normal_grant"].crossSession, ids["manifest"].crossSession, ids["eval_manifest"].crossSession, ids["result"].crossSession, ids["task"].crossSession, ids["attempt"].crossSession, ids["eval_attempt"].crossSession, ids["contract"].crossSession, ids["decision"].crossSession},
		{ids["workspace"].crossWorkspace, ids["session"].crossWorkspace, ids["passport"].crossWorkspace, ids["passport2"].crossWorkspace, ids["version"].crossWorkspace, ids["version2"].crossWorkspace, ids["grant"].crossWorkspace, ids["eval_grant"].crossWorkspace, ids["eval_normal_grant"].crossWorkspace, ids["manifest"].crossWorkspace, ids["eval_manifest"].crossWorkspace, ids["result"].crossWorkspace, ids["task"].crossWorkspace, ids["attempt"].crossWorkspace, ids["eval_attempt"].crossWorkspace, ids["contract"].crossWorkspace, ids["decision"].crossWorkspace},
	}
	for _, scope := range scopes {
		for _, passport := range []struct{ id, kind string }{
			{scope.passport, "claim"}, {scope.passport2, "claim"}, {scope.manifest, "context_manifest"}, {scope.evalManifest, "context_manifest"}, {scope.result, "result_artifact"},
		} {
			if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_passport(id,workspace_id,session_id,entity_kind) VALUES ($1::uuid,$2::uuid,$3::uuid,$4)`, passport.id, scope.workspace, scope.session, passport.kind); err != nil {
				t.Fatal(err)
			}
		}
		for _, version := range []struct{ id, passport string }{{scope.version, scope.passport}, {scope.version2, scope.passport2}} {
			if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_version(id,workspace_id,session_id,artifact_id,version,content_hash,access_level) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','raw')`, version.id, scope.workspace, scope.session, version.passport); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_policy_grant(id,workspace_id,session_id,principal_kind,principal_id,purpose,normal_clearance,evaluation_private,revision,status) VALUES ($1::uuid,$2::uuid,$3::uuid,'agent',$4::uuid,'task_execution','raw',false,1,'active'),($5::uuid,$2::uuid,$3::uuid,'agent',$4::uuid,'evaluation',NULL,true,1,'active'),($6::uuid,$2::uuid,$3::uuid,'agent',$4::uuid,'evaluation','raw',false,1,'active')`, scope.grant, scope.workspace, scope.session, scope.attempt, scope.evalGrant, scope.evalNormalGrant); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_context_manifest(id,workspace_id,session_id,attempt_id,task_id,purpose,normal_grant_id,normal_grant_revision) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'task_execution',$6::uuid,1)`, scope.manifest, scope.workspace, scope.session, scope.attempt, scope.task, scope.grant); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_context_manifest(id,workspace_id,session_id,attempt_id,task_id,purpose,normal_grant_id,normal_grant_revision,evaluation_grant_id,evaluation_grant_revision) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'evaluation',$6::uuid,1,$7::uuid,1)`, scope.evalManifest, scope.workspace, scope.session, scope.evalAttempt, scope.task, scope.evalNormalGrant, scope.evalGrant); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, `INSERT INTO research_result_artifact(id,workspace_id,session_id,attempt_id,content_hash) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`, scope.result, scope.workspace, scope.session, scope.attempt); err != nil {
			t.Fatal(err)
		}
	}

	local := scopes[0]
	if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_context_entry(workspace_id,session_id,manifest_id,ordinal,artifact_version_id,eligibility_revision,representation) VALUES ($1::uuid,$2::uuid,$3::uuid,0,$4::uuid,1,'full')`, local.workspace, local.session, local.manifest, local.version); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_context_omission(workspace_id,session_id,manifest_id,candidate_version_id,ordinal,reason) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,0,'policy_denied')`, local.workspace, local.session, local.manifest, local.version2); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_input_reference(workspace_id,session_id,consumer_version_id,input_version_id,relation,manifest_id) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'uses',$5::uuid)`, local.workspace, local.session, local.version, local.version2, local.manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_supersession(workspace_id,session_id,successor_version_id,superseded_version_id,superseded_artifact_id,reason,decision_id,policy_watermark,old_eligibility_revision,new_eligibility_revision) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'test',$6::uuid,1,1,2)`, local.workspace, local.session, local.version, local.version2, local.passport2, local.decision); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO research_artifact_lifecycle_event(workspace_id,session_id,artifact_id,old_status,new_status,old_eligibility_revision,new_eligibility_revision,policy_watermark,decision_id) VALUES ($1::uuid,$2::uuid,$3::uuid,'registered','accepted',1,2,1,$4::uuid)`, local.workspace, local.session, local.passport, local.decision); err != nil {
		t.Fatal(err)
	}
}

func directScopedFKMutations(ids map[string]scopedFKTarget) []scopedFKMutation {
	return []scopedFKMutation{
		{"result/attempt", `UPDATE research_result_artifact SET attempt_id=$1::uuid WHERE id='38000000-0000-4000-8000-000000000001'`, ids["attempt"]},
		{"manifest/attempt", `UPDATE research_artifact_context_manifest SET attempt_id=$1::uuid WHERE id='37000000-0000-4000-8000-000000000001'`, ids["attempt"]},
		{"manifest/task", `UPDATE research_artifact_context_manifest SET task_id=$1::uuid WHERE id='37000000-0000-4000-8000-000000000001'`, ids["task"]},
		{"manifest/normal-grant", `UPDATE research_artifact_context_manifest SET normal_grant_id=$1::uuid WHERE id='37000000-0000-4000-8000-000000000001'`, ids["grant"]},
		{"manifest/evaluation-grant", `UPDATE research_artifact_context_manifest SET evaluation_grant_id=$1::uuid WHERE id='37100000-0000-4000-8000-000000000001'`, ids["eval_grant"]},
		{"entry/manifest", `UPDATE research_artifact_context_entry SET manifest_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["manifest"]},
		{"entry/version", `UPDATE research_artifact_context_entry SET artifact_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version"]},
		{"omission/manifest", `UPDATE research_artifact_context_omission SET manifest_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["manifest"]},
		{"omission/version", `UPDATE research_artifact_context_omission SET candidate_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version2"]},
		{"input/consumer", `UPDATE research_artifact_input_reference SET consumer_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version"]},
		{"input/input", `UPDATE research_artifact_input_reference SET input_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version2"]},
		{"input/manifest", `UPDATE research_artifact_input_reference SET manifest_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["manifest"]},
		{"supersession/successor", `UPDATE research_artifact_supersession SET successor_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version"]},
		{"supersession/superseded-version", `UPDATE research_artifact_supersession SET superseded_version_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["version2"]},
		{"supersession/passport", `UPDATE research_artifact_supersession SET superseded_artifact_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["passport2"]},
		{"supersession/decision", `UPDATE research_artifact_supersession SET decision_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["decision"]},
		{"lifecycle/passport", `UPDATE research_artifact_lifecycle_event SET artifact_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["passport"]},
		{"lifecycle/decision", `UPDATE research_artifact_lifecycle_event SET decision_id=$1::uuid WHERE workspace_id='10000000-0000-4000-8000-000000000001'::uuid`, ids["decision"]},
	}
}

func directScopedFKInsertMutations(ids map[string]scopedFKTarget) []scopedFKMutation {
	return []scopedFKMutation{
		{"policy-mutation/passport", `INSERT INTO research_artifact_policy_mutation(workspace_id,session_id,watermark,mutation_kind,artifact_id,old_eligibility_revision,new_eligibility_revision) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001',99,'eligibility',$1::uuid,1,2)`, ids["passport"]},
		{"policy-mutation/grant", `INSERT INTO research_artifact_policy_mutation(workspace_id,session_id,watermark,mutation_kind,policy_grant_id,old_grant_revision,new_grant_revision,old_grant_status,new_grant_status) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001',99,'grant_revoke',$1::uuid,1,2,'active','revoked')`, ids["grant"]},
		{"version/passport", `INSERT INTO research_artifact_version(workspace_id,session_id,artifact_id,version,content_hash,access_level) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001',$1::uuid,2,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','raw')`, ids["passport"]},
		{"version/task", `INSERT INTO research_artifact_version(workspace_id,session_id,artifact_id,version,content_hash,access_level,produced_by_task_id) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','34000000-0000-4000-8000-000000000001',2,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','raw',$1::uuid)`, ids["task"]},
		{"version/attempt", `INSERT INTO research_artifact_version(workspace_id,session_id,artifact_id,version,content_hash,access_level,produced_by_attempt_id) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','34000000-0000-4000-8000-000000000001',2,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','raw',$1::uuid)`, ids["attempt"]},
		{"version/contract", `INSERT INTO research_artifact_version(workspace_id,session_id,artifact_id,version,content_hash,access_level,contract_revision_id) VALUES ('10000000-0000-4000-8000-000000000001','20000000-0000-4000-8000-000000000001','34000000-0000-4000-8000-000000000001',2,'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb','raw',$1::uuid)`, ids["contract"]},
	}
}

func assertScopedFKMutation(t *testing.T, ctx context.Context, conn *pgx.Conn, query, target string, wantAllowed bool) {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, execErr := tx.Exec(ctx, query, target)
	if execErr == nil {
		_, execErr = tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`)
	}
	if wantAllowed && execErr != nil {
		t.Fatalf("same-scope control rejected: %v", execErr)
	}
	if !wantAllowed && execErr == nil {
		t.Fatal("cross-scope reference unexpectedly accepted")
	}
}
