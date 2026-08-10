package computer

import (
	"context"
	"testing"
	"time"
)

type fakeUpgradeTransport struct {
	postPath string
	getPath  string
	polls    int
}

func (f *fakeUpgradeTransport) PostJSON(_ context.Context, path string, _ any, out any) error {
	f.postPath = path
	*out.(*map[string]any) = map[string]any{"id": "upgrade-1", "phase": "accepted"}
	return nil
}

func (f *fakeUpgradeTransport) GetJSON(_ context.Context, path string, out any) error {
	f.getPath = path
	f.polls++
	*out.(*map[string]any) = map[string]any{"id": "upgrade-1", "phase": "completed"}
	return nil
}

func TestLifecycleUpgradeOwnsMachinePathAndTerminalPolling(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	identity, err := NewIdentityStore(RootDir("")).CreateFresh()
	if err != nil {
		t.Fatal(err)
	}
	transport := &fakeUpgradeTransport{}
	result, err := (&Lifecycle{}).Upgrade(context.Background(), transport, UpgradeOptions{
		Wait: true, PollInterval: time.Millisecond, RequestID: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/daemons/" + identity.ID + "/upgrades"
	if transport.postPath != base || transport.getPath != base+"/upgrade-1" || transport.polls != 1 {
		t.Fatalf("upgrade transport = post %q get %q polls %d", transport.postPath, transport.getPath, transport.polls)
	}
	if result["phase"] != "completed" {
		t.Fatalf("upgrade result = %#v", result)
	}
}
