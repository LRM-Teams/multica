package computer

import (
	"fmt"
	"net"
	"net/http"
	"testing"
)

func TestRequestShutdownSendsAuditMetadata(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	requests := make(chan *http.Request, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	err = RequestShutdown(fmt.Sprintf("http://127.0.0.1:%d", port), ShutdownRequest{Source: "desktop", Action: "restart", RequestPID: 8123})
	if err != nil {
		t.Fatal(err)
	}
	request := <-requests
	if got := request.Header.Get(shutdownSourceHeader); got != "desktop" {
		t.Fatalf("shutdown source = %q, want desktop", got)
	}
	if got := request.Header.Get(shutdownActionHeader); got != "restart" {
		t.Fatalf("shutdown action = %q, want restart", got)
	}
	if got := request.Header.Get(shutdownRequestPIDHeader); got != "8123" {
		t.Fatalf("shutdown request PID = %q, want 8123", got)
	}
}
