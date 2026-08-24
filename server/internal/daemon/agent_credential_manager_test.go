package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAgentCredentialManagerCoalescesConcurrentRefresh(t *testing.T) {
	root := t.TempDir()
	var ensureCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ensureCalls.Add(1)
		time.Sleep(50 * time.Millisecond)
		expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
		_ = json.NewEncoder(w).Encode(AgentCredentialResponse{
			ID: "credential-2", AgentID: "agent-1", Token: "refreshed-token", ExpiresAt: &expiresAt,
		})
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-1", "agent-1", AgentCredentialResponse{
		ID: "credential-1", AgentID: "agent-1", Token: "near-expiry-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken("runtime-1", "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{cfg: cfg, client: client}
	key := agentCredentialKey{WorkspaceID: "workspace-1", RuntimeID: "runtime-1", AgentID: "agent-1"}

	const callers = 20
	start := make(chan struct{})
	errors := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			credential, err := d.credentialManager().Get(context.Background(), key, agentCredentialCacheFirst)
			if err == nil && credential.Token != "refreshed-token" {
				err = fmt.Errorf("credential token = %q, want refreshed token", credential.Token)
			}
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := ensureCalls.Load(); got != 1 {
		t.Fatalf("credential ensure calls = %d, want 1", got)
	}
}

func TestAgentCredentialManagerRejectsFreshCredentialFromAnotherRuntime(t *testing.T) {
	root := t.TempDir()
	var ensureCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ensureCalls.Add(1)
		if r.URL.Path != "/api/daemon/runtimes/runtime-new/agents/agent-1/credential" {
			t.Errorf("ensure path = %q", r.URL.Path)
		}
		expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
		_ = json.NewEncoder(w).Encode(AgentCredentialResponse{
			ID: "credential-new", AgentID: "agent-1", Token: "runtime-new-token", ExpiresAt: &expiresAt,
		})
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, "workspace-1", "runtime-old", "agent-1", AgentCredentialResponse{
		ID: "credential-old", AgentID: "agent-1", Token: "runtime-old-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken("runtime-new", "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{cfg: cfg, client: client}
	credential, err := d.credentialManager().Get(context.Background(), agentCredentialKey{
		WorkspaceID: "workspace-1", RuntimeID: "runtime-new", AgentID: "agent-1",
	}, agentCredentialCacheFirst)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if credential.Token != "runtime-new-token" || ensureCalls.Load() != 1 {
		t.Fatalf("credential token=%q ensure calls=%d", credential.Token, ensureCalls.Load())
	}
}

func TestAgentCredentialManagerLeaderCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var ensureCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ensureCalls.Add(1)
		close(started)
		<-release
		expiresAt := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)
		_ = json.NewEncoder(w).Encode(AgentCredentialResponse{
			ID: "credential-1", AgentID: "agent-1", Token: "shared-token", ExpiresAt: &expiresAt,
		})
	}))
	defer upstream.Close()

	client := NewClient(upstream.URL)
	client.SetRuntimeDaemonToken("runtime-1", "daemon-token", time.Now().Add(time.Hour))
	d := &Daemon{cfg: Config{WorkspacesRoot: root, ServerBaseURL: upstream.URL}, client: client}
	key := agentCredentialKey{WorkspaceID: "workspace-1", RuntimeID: "runtime-1", AgentID: "agent-1"}
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := d.credentialManager().Get(leaderCtx, key, agentCredentialCacheFirst)
		leaderErr <- err
	}()
	<-started
	follower := make(chan cachedAgentCredential, 1)
	followerErr := make(chan error, 1)
	go func() {
		credential, err := d.credentialManager().Get(context.Background(), key, agentCredentialCacheFirst)
		follower <- credential
		followerErr <- err
	}()
	cancelLeader()
	if err := <-leaderErr; err != context.Canceled {
		t.Fatalf("leader error = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-followerErr; err != nil {
		t.Fatalf("follower Get: %v", err)
	}
	if credential := <-follower; credential.Token != "shared-token" {
		t.Fatalf("follower token = %q", credential.Token)
	}
	if got := ensureCalls.Load(); got != 1 {
		t.Fatalf("credential ensure calls = %d, want 1", got)
	}
}

func TestAgentCredentialManagerDaemonCancellationStopsSharedRefresh(t *testing.T) {
	root := t.TempDir()
	started := make(chan struct{})
	requestCanceled := make(chan struct{})
	client := NewClient("https://api.example.test")
	client.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		close(requestCanceled)
		return nil, request.Context().Err()
	})
	client.SetRuntimeDaemonToken("runtime-1", "daemon-token", time.Now().Add(time.Hour))
	daemonCtx, cancelDaemon := context.WithCancel(context.Background())
	d := &Daemon{
		cfg: Config{WorkspacesRoot: root, ServerBaseURL: "https://api.example.test"}, client: client, rootCtx: daemonCtx,
	}
	result := make(chan error, 1)
	go func() {
		_, err := d.credentialManager().Get(context.Background(), agentCredentialKey{
			WorkspaceID: "workspace-1", RuntimeID: "runtime-1", AgentID: "agent-1",
		}, agentCredentialCacheFirst)
		result <- err
	}()
	<-started
	cancelDaemon()
	if err := <-result; err == nil {
		t.Fatal("Get succeeded after daemon cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("upstream ensure request survived daemon cancellation")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
