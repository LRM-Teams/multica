package service

import (
	"context"
	"reflect"
	"testing"
)

func TestSendCanonicalChannelMessagePublishesOnlyAfterAtomicPersistenceAndPostAck(t *testing.T) {
	var order []string
	operation := CanonicalChannelMessageOperation[string, string]{
		Validate: func(context.Context) error {
			order = append(order, "validate")
			return nil
		},
		PersistAtomically: func(context.Context) (CanonicalChannelMessagePersistence[string, string], error) {
			order = append(order, "atomic-message-recipients-obligations")
			return CanonicalChannelMessagePersistence[string, string]{
				Message: "message-1", Created: true, Recipients: []string{"agent-1", "agent-2"},
			}, nil
		},
		Publish: func(context.Context, string) error {
			order = append(order, "publish")
			return nil
		},
		PostAck: func(_ context.Context, message string, created bool, recipients []string) {
			if message != "message-1" || !created || !reflect.DeepEqual(recipients, []string{"agent-1", "agent-2"}) {
				t.Fatalf("post-ack got message=%q created=%v recipients=%v", message, created, recipients)
			}
			order = append(order, "post-ack")
		},
	}

	result, err := SendCanonicalChannelMessage(context.Background(), operation)
	if err != nil {
		t.Fatalf("send canonical message: %v", err)
	}
	if got, want := order, []string{"validate", "atomic-message-recipients-obligations", "publish"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order before acknowledgment = %v, want %v", got, want)
	}
	result.Acknowledge(context.Background())
	result.Acknowledge(context.Background())
	if got, want := order, []string{"validate", "atomic-message-recipients-obligations", "publish", "post-ack"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order after duplicate acknowledgment = %v, want %v", got, want)
	}
}

func TestSendCanonicalChannelMessageReplayRepairsAtomicallyWithoutPublish(t *testing.T) {
	var persisted, published, acknowledged int
	result, err := SendCanonicalChannelMessage(context.Background(), CanonicalChannelMessageOperation[string, string]{
		PersistAtomically: func(context.Context) (CanonicalChannelMessagePersistence[string, string], error) {
			persisted++
			return CanonicalChannelMessagePersistence[string, string]{
				Message: "message-1", Created: false, Recipients: []string{"repaired-agent"},
			}, nil
		},
		Publish: func(context.Context, string) error {
			published++
			return nil
		},
		PostAck: func(_ context.Context, _ string, created bool, recipients []string) {
			if created || !reflect.DeepEqual(recipients, []string{"repaired-agent"}) {
				t.Fatalf("replay post-ack created=%v recipients=%v", created, recipients)
			}
			acknowledged++
		},
	})
	if err != nil {
		t.Fatalf("replay canonical message: %v", err)
	}
	result.Acknowledge(context.Background())
	if persisted != 1 || published != 0 || acknowledged != 1 {
		t.Fatalf("replay stages persisted=%d published=%d acknowledged=%d, want 1,0,1", persisted, published, acknowledged)
	}
}
