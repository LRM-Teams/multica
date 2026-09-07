package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// The managed Graph Memory gateway CLI. Stock pi has no MCP support, so the
// memory agent reaches the gateway through this command from bash; requests
// authenticate via the launch-scoped Agent Proxy wrapper like every other
// agent-side CLI command, and the agent never holds a server credential.
var graphMemoryCmd = &cobra.Command{
	Use:   "graph-memory",
	Short: "Run managed Graph Memory gateway operations",
}

var graphMemoryStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a bounded Graph Memory run for a channel",
	RunE: func(cmd *cobra.Command, _ []string) error {
		query, _ := cmd.Flags().GetString("query")
		body, err := json.Marshal(map[string]any{
			"query": query, "idempotency_key": graphMemoryCLIIdempotencyKey(cmd),
		})
		if err != nil {
			return err
		}
		return graphMemoryCLICall(cmd, "start", body)
	},
}

var graphMemoryExploreCmd = &cobra.Command{
	Use:   "explore",
	Short: "Inspect node ids or MemoryRef values returned by Graph Memory",
	RunE: func(cmd *cobra.Command, _ []string) error {
		body := map[string]any{
			"trajectory_id": graphMemoryCLITrajectory(cmd), "idempotency_key": graphMemoryCLIIdempotencyKey(cmd),
		}
		if nodeIDs := graphMemoryCLINodeIDs(cmd); len(nodeIDs) > 0 {
			body["node_ids"] = nodeIDs
		}
		if ref := graphMemoryCLIRef(cmd); ref != nil {
			body["ref"] = ref
		}
		if _, ok := body["node_ids"]; !ok {
			if _, hasRef := body["ref"]; !hasRef {
				return fmt.Errorf("--node-ids or --ref is required")
			}
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		return graphMemoryCLICall(cmd, "explore", encoded)
	},
}

var graphMemoryRedirectCmd = &cobra.Command{
	Use:   "redirect",
	Short: "Redirect an active run after a directed correction",
	RunE: func(cmd *cobra.Command, _ []string) error {
		steering, _ := cmd.Flags().GetString("steering-message")
		query, _ := cmd.Flags().GetString("query")
		body, err := json.Marshal(map[string]any{
			"trajectory_id": graphMemoryCLITrajectory(cmd), "query": query,
			"steering_message_id": steering, "idempotency_key": graphMemoryCLIIdempotencyKey(cmd),
		})
		if err != nil {
			return err
		}
		return graphMemoryCLICall(cmd, "redirect", body)
	},
}

var graphMemorySubmitCmd = &cobra.Command{
	Use:   "submit",
	Short: "Submit a completed Graph Memory run",
	RunE: func(cmd *cobra.Command, _ []string) error {
		body := map[string]any{
			"trajectory_id": graphMemoryCLITrajectory(cmd), "idempotency_key": graphMemoryCLIIdempotencyKey(cmd),
		}
		if found, _ := cmd.Flags().GetBool("found"); found {
			body["found"] = true
		}
		if summary, _ := cmd.Flags().GetString("summary"); strings.TrimSpace(summary) != "" {
			body["summary"] = summary
		}
		if nodeIDs := graphMemoryCLINodeIDs(cmd); len(nodeIDs) > 0 {
			body["node_ids"] = nodeIDs
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		return graphMemoryCLICall(cmd, "submit", encoded)
	},
}

var graphMemoryCheckpointCmd = &cobra.Command{
	Use:   "checkpoint",
	Short: "Checkpoint an active Graph Memory run",
	RunE: func(cmd *cobra.Command, _ []string) error {
		body, err := json.Marshal(map[string]any{
			"trajectory_id": graphMemoryCLITrajectory(cmd), "idempotency_key": graphMemoryCLIIdempotencyKey(cmd),
		})
		if err != nil {
			return err
		}
		return graphMemoryCLICall(cmd, "checkpoint", body)
	},
}

func init() {
	graphMemoryCmd.AddCommand(graphMemoryStartCmd, graphMemoryExploreCmd, graphMemoryRedirectCmd, graphMemorySubmitCmd, graphMemoryCheckpointCmd)
	for _, cmd := range []*cobra.Command{graphMemoryStartCmd, graphMemoryExploreCmd, graphMemoryRedirectCmd, graphMemorySubmitCmd, graphMemoryCheckpointCmd} {
		cmd.Flags().String("channel", "", "Managed channel id")
		_ = cmd.MarkFlagRequired("channel")
		cmd.Flags().String("idempotency-key", "", "Stable key unique to this message and operation")
		_ = cmd.MarkFlagRequired("idempotency-key")
		cmd.SilenceUsage = true
	}
	graphMemoryStartCmd.Flags().String("query", "", "Recall query; start with the user request")
	_ = graphMemoryStartCmd.MarkFlagRequired("query")
	for _, cmd := range []*cobra.Command{graphMemoryExploreCmd, graphMemoryRedirectCmd, graphMemorySubmitCmd, graphMemoryCheckpointCmd} {
		cmd.Flags().String("trajectory", "", "Active trajectory id returned by start")
		_ = cmd.MarkFlagRequired("trajectory")
	}
	graphMemoryExploreCmd.Flags().String("node-ids", "", "Comma-separated node ids from earlier responses")
	graphMemoryExploreCmd.Flags().String("ref", "", "MemoryRef object as JSON")
	graphMemoryRedirectCmd.Flags().String("query", "", "Revised query after the correction")
	_ = graphMemoryRedirectCmd.MarkFlagRequired("query")
	graphMemoryRedirectCmd.Flags().String("steering-message", "", "Id of the steering message")
	_ = graphMemoryRedirectCmd.MarkFlagRequired("steering-message")
	graphMemorySubmitCmd.Flags().Bool("found", false, "Mark the run as found")
	graphMemorySubmitCmd.Flags().String("summary", "", "Cited visible memory summary")
	graphMemorySubmitCmd.Flags().String("node-ids", "", "Comma-separated cited node ids")
}

func graphMemoryCLICall(cmd *cobra.Command, operation string, body []byte) error {
	channelID, _ := cmd.Flags().GetString("channel")
	bridge, err := newGraphMemoryMCPBridgeFromEnv()
	if err != nil {
		return fmt.Errorf("graph-memory %s: %w", operation, err)
	}
	response, err := bridge.callGateway(operation, strings.TrimSpace(channelID), body)
	if err != nil {
		return fmt.Errorf("graph-memory %s: %w", operation, err)
	}
	fmt.Println(response)
	return nil
}

func graphMemoryCLIIdempotencyKey(cmd *cobra.Command) string {
	key, _ := cmd.Flags().GetString("idempotency-key")
	return strings.TrimSpace(key)
}

func graphMemoryCLITrajectory(cmd *cobra.Command) string {
	trajectory, _ := cmd.Flags().GetString("trajectory")
	return strings.TrimSpace(trajectory)
}

func graphMemoryCLINodeIDs(cmd *cobra.Command) []string {
	raw, _ := cmd.Flags().GetString("node-ids")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			ids = append(ids, trimmed)
		}
	}
	return ids
}

func graphMemoryCLIRef(cmd *cobra.Command) json.RawMessage {
	raw, _ := cmd.Flags().GetString("ref")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return nil
	}
	return json.RawMessage(raw)
}
