package researchrun

import (
	"encoding/json"
	"fmt"
	"strings"
)

type v6WorkDispatchIdentity struct {
	ManifestID     string          `json:"manifest_id"`
	ManifestHash   string          `json:"manifest_hash"`
	RunID          string          `json:"run_id"`
	WorkItemID     string          `json:"work_item_id"`
	AttemptID      string          `json:"attempt_id"`
	MissionPrompt  string          `json:"mission_prompt"`
	ExpectedResult V6ContractKind  `json:"expected_result_schema"`
	CatalogAccess  json.RawMessage `json:"catalog_access"`
}

// BuildV6WorkDispatchPrompt turns a frozen Work Manifest into an executable
// task prompt. The manifest remains the authority; the prompt only exposes the
// task-bound CLI sequence needed to read, acknowledge, and submit it.
func BuildV6WorkDispatchPrompt(manifest V6WorkManifest) (string, error) {
	decoded, err := DecodeV6Contract(manifest.Bytes, V6ContractWorkManifest, nil)
	if err != nil {
		return "", err
	}
	var identity v6WorkDispatchIdentity
	if err = json.Unmarshal(decoded.Envelope, &identity); err != nil {
		return "", fmt.Errorf("%w: decode V6 work identity", ErrInvalidContract)
	}
	if strings.TrimSpace(identity.RunID) == "" || strings.TrimSpace(identity.WorkItemID) == "" || strings.TrimSpace(identity.AttemptID) == "" ||
		strings.TrimSpace(identity.ManifestID) == "" || strings.TrimSpace(identity.ManifestHash) == "" || strings.TrimSpace(string(identity.ExpectedResult)) == "" {
		return "", fmt.Errorf("%w: incomplete V6 work identity", ErrInvalidContract)
	}

	base := fmt.Sprintf("%s %s %s", identity.RunID, identity.WorkItemID, identity.AttemptID)
	var prompt strings.Builder
	prompt.WriteString("## Durable Research V6 Work Item\n\n")
	prompt.WriteString("Use the `multica-research-fleet` skill. This is a task-bound V6 assignment; chat output does not complete it.\n\n")
	fmt.Fprintf(&prompt, "- Run ID: `%s`\n- Work Item ID: `%s`\n- Attempt ID: `%s`\n", identity.RunID, identity.WorkItemID, identity.AttemptID)
	fmt.Fprintf(&prompt, "- Manifest ID: `%s`\n- Manifest hash: `%s`\n- Expected result: `%s`\n\n", identity.ManifestID, identity.ManifestHash, identity.ExpectedResult)
	prompt.WriteString("Read the frozen authority first:\n\n```bash\nmultica research work-manifest " + base + " --output json\n```\n\n")
	prompt.WriteString("If the installed daemon CLI reports that a V6 command is unknown, use its credential proxy without exposing any token:\n\n")
	prompt.WriteString("```bash\nV6_API=\"http://127.0.0.1:${MULTICA_DAEMON_PORT}/api/agent/research/sessions/" + identity.RunID + "/work-items/" + identity.WorkItemID + "/attempts/" + identity.AttemptID + "\"\n")
	prompt.WriteString("V6_CURL=(curl -fsS -H \"X-Agent-ID: ${MULTICA_AGENT_ID}\" -H \"X-Workspace-ID: ${MULTICA_WORKSPACE_ID}\")\n")
	prompt.WriteString("\"${V6_CURL[@]}\" \"${V6_API}/manifest\"\n```\n\n")
	prompt.WriteString("The fallback uses the same suffixes as the CLI: GET `/director-brief`, POST `/director-brief-acks`, GET `/catalog`, POST `/catalog-acks`, POST `/submission`, and the `/report-uploads` workflow. Send JSON with `Content-Type: application/json`; submit files with `--data-binary @file`.\n\n")
	if identity.ExpectedResult == V6ContractDirectorActionProposal {
		prompt.WriteString(RonaldoV6DirectorSystemProtocol + "\n\n")
		prompt.WriteString("Read every Director Brief page, acknowledge each page with its exact IDs and hashes, then submit the proposal:\n\n")
		prompt.WriteString("```bash\nmultica research director-brief " + base + " --output json\nmultica research director-brief-ack " + base + " --client-request-id <uuid> --brief-id <brief-id> --brief-hash <brief-hash> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
	}
	if len(identity.CatalogAccess) > 0 && string(identity.CatalogAccess) != "null" {
		prompt.WriteString("Read and acknowledge every authorized catalog page needed by the work:\n\n")
		prompt.WriteString("```bash\nmultica research work-catalog " + base + " --view same_tier --output json\nmultica research work-catalog-ack " + base + " --client-request-id <uuid> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
	}
	if identity.ExpectedResult == V6ContractReportPackageSubmission {
		prompt.WriteString("Upload each report resource before referencing its returned resource ID:\n\n")
		prompt.WriteString("```bash\nmultica research report-upload " + base + " --file <absolute-file> --path <package-path> --role <role> --media-type <media-type> --output json\n```\n\n")
	}
	prompt.WriteString("Write exactly the envelope named by `expected_result_schema`, preserving every identity and hash from the manifest, then submit it:\n\n")
	prompt.WriteString("```bash\nmultica research work-submit " + base + " --file <absolute-result.json> --output json\n```\n\n")
	prompt.WriteString("Do not claim completion until `work-submit` returns an accepted outcome.\n\n### Mission\n\n")
	prompt.WriteString(strings.TrimSpace(identity.MissionPrompt))
	prompt.WriteString("\n")
	return prompt.String(), nil
}
