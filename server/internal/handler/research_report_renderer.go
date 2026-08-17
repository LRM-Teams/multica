package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/multica-ai/multica/server/internal/researchrun"
)

type isolatedReportRenderer struct {
	endpoint string
	token    string
	client   *http.Client
}

func newResearchReportRenderer(cfg Config) researchrun.ReportRenderAdapter {
	parsed, err := url.Parse(cfg.ResearchReportRendererURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(cfg.ResearchReportRendererToken) < 32 {
		return nil
	}
	return &isolatedReportRenderer{
		endpoint: parsed.String() + "/v1/render-report",
		token:    cfg.ResearchReportRendererToken,
		client: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}
}

func (r *isolatedReportRenderer) RenderReport(ctx context.Context, input researchrun.ReportRenderInput) (researchrun.ReportRenderResult, error) {
	body, err := json.Marshal(map[string]any{"html": input.HTML, "csp": input.CSP, "network_policy": "deny_all", "storage_policy": "ephemeral_empty", "timeout_ms": 30000})
	if err != nil {
		return researchrun.ReportRenderResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return researchrun.ReportRenderResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(req)
	if err != nil {
		return researchrun.ReportRenderResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return researchrun.ReportRenderResult{}, fmt.Errorf("isolated report renderer returned %s", response.Status)
	}
	var result researchrun.ReportRenderResult
	decoder := json.NewDecoder(io.LimitReader(response.Body, 12<<20))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&result); err != nil || len(result.Screenshot) > 10<<20 || len(result.Diagnostics) > 256<<10 {
		return researchrun.ReportRenderResult{}, fmt.Errorf("invalid isolated report renderer response")
	}
	return result, nil
}
