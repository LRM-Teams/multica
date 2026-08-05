package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectResearchFineDomain(t *testing.T) {
	cases := []struct {
		goal string
		want string
	}{
		{"想做一款浏览器传奇页游，Unity 还是自研引擎？", researchDomainGame},
		{"评估 LLM agent 训练与推理成本，对比 HuggingFace 开源栈", researchDomainAIEngineering},
		{"写一篇 arXiv 论文综述：多智能体协作的方法局限", researchDomainAcademicPapers},
		{"投研终端能否对散户给买卖建议？合规与行情数据成本", researchDomainFinance},
		{"品牌视觉与 illustration 交付，Behance 风格参照与人机边界", researchDomainDesignVisual},
	}
	for _, tc := range cases {
		if got := detectResearchFineDomain(tc.goal); got != tc.want {
			t.Fatalf("goal %q: got %q want %q", tc.goal, got, tc.want)
		}
	}
}

func TestAdaptivePlansDifferAcrossDomains(t *testing.T) {
	game := buildResearchAdaptivePlan("开发一款 Steam 独立游戏并上线")
	finance := buildResearchAdaptivePlan("做一款金融投研合规助手并落地")
	if game.FineDomain != researchDomainGame {
		t.Fatalf("game domain = %q", game.FineDomain)
	}
	if finance.FineDomain != researchDomainFinance {
		t.Fatalf("finance domain = %q", finance.FineDomain)
	}
	if game.Dimensions[0].Title == finance.Dimensions[0].Title {
		t.Fatal("expected different first-wave dimension titles across domains")
	}
	gameHints := strings.Join(game.SourceClasses, ",")
	finHints := strings.Join(finance.SourceClasses, ",")
	if gameHints == finHints {
		t.Fatal("expected different source class strategies")
	}
	if !game.DeliveryLike || !finance.DeliveryLike {
		t.Fatal("build goals should retain delivery context")
	}
	if game.HumanAIBoundaryRequired || finance.HumanAIBoundaryRequired {
		t.Fatal("build verbs alone must not manufacture a human/AI requirement")
	}
	explicit := buildResearchAdaptivePlan("开发一款需要人工审批 AI 输出的游戏")
	if !explicit.HumanAIBoundaryRequired {
		t.Fatal("an explicit human/AI responsibility question must be preserved")
	}
	hasBoundary := false
	for _, d := range explicit.Dimensions {
		if d.Family == "human_ai_boundary" && d.Required {
			hasBoundary = true
		}
	}
	if !hasBoundary {
		t.Fatal("explicit human/AI goal must require the corresponding dimension")
	}
}

func TestResearchDomainPlaybooksFineDomains(t *testing.T) {
	books := researchDomainPlaybooks()
	for _, domain := range []string{
		"general", "tech", "market", "academic",
		researchDomainGame, researchDomainAIEngineering, researchDomainAcademicPapers,
		researchDomainFinance, researchDomainDesignVisual,
	} {
		if books[domain] == "" {
			t.Fatalf("missing playbook %s", domain)
		}
		for _, required := range []string{"## decision", "## method", "failure"} {
			if !strings.Contains(strings.ToLower(books[domain]), required) && domain != "general" {
				t.Fatalf("playbook %s missing adaptation section %q", domain, required)
			}
		}
	}
}

func TestMergeSourceWhyPayload(t *testing.T) {
	raw := mergeSourceWhyPayload(json.RawMessage(`{"note":"x"}`), "引擎官方文档覆盖管线", "feasibility")
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	if obj["why"] != "引擎官方文档覆盖管线" {
		t.Fatalf("why=%v", obj["why"])
	}
	if obj["dimension_family"] != "feasibility" {
		t.Fatalf("dimension_family=%v", obj["dimension_family"])
	}
	if obj["note"] != "x" {
		t.Fatal("expected existing payload keys preserved")
	}
	if sourceWhyFromPayload(raw) == "" {
		t.Fatal("sourceWhyFromPayload empty")
	}
}

func TestReportHasHumanAIBoundary(t *testing.T) {
	ok := reportHasHumanAIBoundary("本报告标注 AI 上限与必须有人环节，并比较人做 vs AI 做。")
	if !ok {
		t.Fatal("expected boundary detection")
	}
	if reportHasHumanAIBoundary("只有一句结论") {
		t.Fatal("thin report should not pass")
	}
}

func TestAdaptiveKickoffLeadPrompt(t *testing.T) {
	plan := buildResearchAdaptivePlan("制作一款需要人工审批 AI 资产的页游传奇")
	prompt := adaptiveKickoffLeadPrompt("制作一款需要人工审批 AI 资产的页游传奇", plan)
	for _, needle := range []string{"Tentative domain profile: game", "payload.why", "human/AI responsibility", "Candidate dimensions", "fixed source count"} {
		if !strings.Contains(prompt, needle) {
			t.Fatalf("missing %q in prompt", needle)
		}
	}
}
