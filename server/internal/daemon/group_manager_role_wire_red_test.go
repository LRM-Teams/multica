package daemon

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTaskAgentDataCarriesManagerChannelsAcrossClaimWire(t *testing.T) {
	var task Task
	if err := json.Unmarshal([]byte(`{
		"agent": {
			"id": "derived-agent",
			"name": "manager",
			"manager_channels": [
				{"id": "channel-a", "name": "a"},
				{"id": "channel-b", "name": "b"}
			]
		}
	}`), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if task.Agent == nil {
		t.Fatal("decoded task agent is nil")
	}

	field := reflect.ValueOf(task.Agent).Elem().FieldByName("ManagerChannels")
	if !field.IsValid() {
		t.Fatal("daemon AgentData.ManagerChannels production field is not implemented")
	}
	if field.Kind() != reflect.Slice || field.Len() != 2 {
		t.Fatalf("ManagerChannels kind=%s len=%d want slice len=2", field.Kind(), field.Len())
	}
	for index, want := range []struct {
		id   string
		name string
	}{
		{id: "channel-a", name: "a"},
		{id: "channel-b", name: "b"},
	} {
		item := field.Index(index)
		if item.Kind() == reflect.Pointer {
			if item.IsNil() {
				t.Fatalf("ManagerChannels[%d] is nil", index)
			}
			item = item.Elem()
		}
		if item.Kind() != reflect.Struct {
			t.Fatalf("ManagerChannels[%d] kind=%s want struct", index, item.Kind())
		}
		if got := item.FieldByName("ID").String(); got != want.id {
			t.Fatalf("ManagerChannels[%d].ID=%q want %q", index, got, want.id)
		}
		if got := item.FieldByName("Name").String(); got != want.name {
			t.Fatalf("ManagerChannels[%d].Name=%q want %q", index, got, want.name)
		}
	}
}
