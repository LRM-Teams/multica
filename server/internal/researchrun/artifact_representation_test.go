package researchrun

import (
	"errors"
	"testing"
)

func TestDecodeFrozenRepresentationVerifiesModeBytesAndHash(t *testing.T) {
	encoded := []byte(`{"id":"source-1"}`)
	hash := contentHashFromPayload(encoded)
	var decoded struct {
		ID string `json:"id"`
	}
	if err := decodeFrozenRepresentation("full", encoded, hash, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != "source-1" {
		t.Fatalf("decoded=%+v", decoded)
	}
	for _, tc := range []struct {
		name           string
		representation string
		bytes          []byte
		storedHash     string
	}{
		{name: "wrong mode", representation: "summary", bytes: encoded, storedHash: hash},
		{name: "missing bytes", representation: "full", storedHash: hash},
		{name: "hash mismatch", representation: "full", bytes: []byte(`{"id":"tampered"}`), storedHash: hash},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := decodeFrozenRepresentation(tc.representation, tc.bytes, tc.storedHash, &decoded); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
