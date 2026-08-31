package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// consumeInboxResponse writes the canonical response only after it has been
// decoded. Inbox retirement is performed by the daemon using the item
// revision returned in that response; no second receipt protocol exists.
func consumeInboxResponse(_ context.Context, r io.Reader, w io.Writer, out any, writeOutput func(io.Writer) error) error {
	if r == nil || writeOutput == nil {
		return fmt.Errorf("inbox response is unavailable")
	}
	if err := json.NewDecoder(io.LimitReader(r, 1<<20)).Decode(out); err != nil {
		return fmt.Errorf("decode inbox response: %w", err)
	}
	if err := writeOutput(w); err != nil {
		return fmt.Errorf("write inbox output: %w", err)
	}
	return nil
}
