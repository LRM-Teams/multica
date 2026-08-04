package handler

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Fine-grained adaptive domains (LRM-883 / LRM-888). Coarse tech/market/academic
// seeds remain; these map onto them and drive kickoff dimension trees.
const (
	researchDomainGame           = "game"
	researchDomainAIEngineering  = "ai_engineering"
	researchDomainAcademicPapers = "academic_papers"
	researchDomainFinance        = "finance"
	researchDomainDesignVisual   = "design_visual"
	researchDomainGeneral        = "general"
)

type researchDimensionSeed struct {
	Family      string
	Title       string
	Summary     string
	SourceHints []string
	Required    bool
}

type researchAdaptivePlan struct {
	FineDomain              string
	CoarseDomains           []string
	DeliveryLike            bool
	HumanAIBoundaryRequired bool
	Dimensions              []researchDimensionSeed
	PlaybookMD              string
	SourceClasses           []string
}

// detectResearchFineDomain picks the primary fine domain from the user goal.
// Multi-label is allowed later; kickoff uses the strongest single domain.
func detectResearchFineDomain(goal string) string {
	g := strings.ToLower(strings.TrimSpace(goal))
	type hit struct {
		domain string
		score  int
	}
	scores := []hit{
		{researchDomainGame, countKeywordHits(g, []string{
			"游戏", "game", "页游", "网游", "传奇", "unity", "unreal", "godot", "steam", "mmorpg", "玩法", "关卡",
		})},
		{researchDomainAIEngineering, countKeywordHits(g, []string{
			"ai", "ml", "llm", "模型", "训练", "推理", "agent", "机器学习", "深度学习", "huggingface", "gpu",
		})},
		{researchDomainAcademicPapers, countKeywordHits(g, []string{
			"论文", "paper", "arxiv", "调研综述", "文献", "peer-reviewed", "学术", "doi", "会议",
		})},
		{researchDomainFinance, countKeywordHits(g, []string{
			"金融", "投研", "证券", "基金", "合规", "监管", "行情", "交易", "finance", "trading", "券商",
		})},
		{researchDomainDesignVisual, countKeywordHits(g, []string{
			"设计", "视觉", "ui", "ux", "绘图", "品牌", "design", "illustration", "behance", "dribbble", "字体",
		})},
	}
	best := researchDomainGeneral
	bestScore := 0
	for _, h := range scores {
		if h.score > bestScore {
			bestScore = h.score
			best = h.domain
		}
	}
	return best
}

func countKeywordHits(haystack string, keys []string) int {
	n := 0
	for _, k := range keys {
		if strings.Contains(haystack, strings.ToLower(k)) {
			n++
		}
	}
	return n
}

func researchGoalLooksDeliveryLike(goal string) bool {
	g := strings.ToLower(goal)
	for _, k := range []string{"做", "制作", "开发", "落地", "实现", "搭建", "build", "ship", "launch", "implement"} {
		if strings.Contains(g, k) {
			return true
		}
	}
	return false
}

func researchGoalRequiresHumanAIBoundary(goal string) bool {
	g := strings.ToLower(goal)
	for _, phrase := range []string{
		"人机", "人工", "人做", "ai 做", "ai做", "human-in-the-loop", "human in the loop",
		"human vs ai", "human-ai", "自动化边界", "人工审核", "人工审批",
	} {
		if strings.Contains(g, phrase) {
			return true
		}
	}
	return false
}

func coarseDomainsForFine(fine string) []string {
	switch fine {
	case researchDomainGame:
		return []string{"tech", "market"}
	case researchDomainAIEngineering:
		return []string{"tech"}
	case researchDomainAcademicPapers:
		return []string{"academic"}
	case researchDomainFinance:
		return []string{"market", "academic"}
	case researchDomainDesignVisual:
		return []string{"market", "tech"}
	default:
		return []string{"general"}
	}
}

// buildResearchAdaptivePlan returns domain-sensitive dimension seeds.
// Seeds are dimension *types* + domain-flavored titles — never a fixed user Q&A list.
func buildResearchAdaptivePlan(goal string) researchAdaptivePlan {
	fine := detectResearchFineDomain(goal)
	delivery := researchGoalLooksDeliveryLike(goal)
	humanAIBoundaryRequired := researchGoalRequiresHumanAIBoundary(goal)
	plan := researchAdaptivePlan{
		FineDomain:              fine,
		CoarseDomains:           coarseDomainsForFine(fine),
		DeliveryLike:            delivery,
		HumanAIBoundaryRequired: humanAIBoundaryRequired,
		PlaybookMD:              researchFineDomainPlaybook(fine),
		SourceClasses:           researchFineDomainSourceClasses(fine),
	}
	plan.Dimensions = researchDimensionSeedsForDomain(fine, delivery, humanAIBoundaryRequired)
	return plan
}

func researchFineDomainSourceClasses(fine string) []string {
	switch fine {
	case researchDomainGame:
		return []string{"docs", "github", "product", "community", "x", "marketplace"}
	case researchDomainAIEngineering:
		return []string{"github", "arxiv", "docs", "benchmark", "x", "blog"}
	case researchDomainAcademicPapers:
		return []string{"arxiv", "journal", "dataset", "github", "lab_blog"}
	case researchDomainFinance:
		return []string{"regulator", "ir", "research_note", "news", "blog", "github"}
	case researchDomainDesignVisual:
		return []string{"portfolio", "docs", "x", "marketplace", "github", "case_study"}
	default:
		return []string{"web", "github", "x", "docs"}
	}
}

func researchDimensionSeedsForDomain(fine string, deliveryLike, humanAIBoundaryRequired bool) []researchDimensionSeed {
	// Cap first-wave dimensions at "standard" depth (6–7) per LRM-883 §2.3.
	switch fine {
	case researchDomainGame:
		return []researchDimensionSeed{
			{Family: "problem_definition", Title: "品类与成功标准", Summary: "核心循环、胜负条件、非目标边界（随目标生成具体子问，勿套固定题库）", SourceHints: []string{"product", "community"}, Required: true},
			{Family: "feasibility", Title: "引擎与浏览器/联网约束", Summary: "技术路径、管线与当前条件缺口", SourceHints: []string{"docs", "github"}, Required: true},
			{Family: "resources", Title: "美术/音频资产供给", Summary: "自制 / 采购 / AI 生成三支取证", SourceHints: []string{"marketplace", "x"}, Required: deliveryLike},
			{Family: "human_ai_boundary", Title: "人机制作边界", Summary: "仅在目标涉及自动化分工时评估审批、创作和运营责任", SourceHints: []string{"case_study", "community"}, Required: humanAIBoundaryRequired},
			{Family: "precedents", Title: "可运行先例与开源示例", Summary: "长什么样、可复用什么", SourceHints: []string{"github", "product"}, Required: true},
			{Family: "cost_schedule", Title: "成本与周期区间", Summary: "成本驱动与粗算人天/外包/订阅", SourceHints: []string{"marketplace", "x"}, Required: deliveryLike},
			{Family: "risks", Title: "合规与版权风险", Summary: "版号/素材/音乐授权等失败模式", SourceHints: []string{"docs", "blog"}, Required: deliveryLike},
		}
	case researchDomainAIEngineering:
		return []researchDimensionSeed{
			{Family: "problem_definition", Title: "问题是否可 ML 化", Summary: "成功标准与不可自动化边界", SourceHints: []string{"docs", "arxiv"}, Required: true},
			{Family: "resources", Title: "数据可得性与标注", Summary: "公开/可合成/需采购数据路径", SourceHints: []string{"dataset", "github"}, Required: true},
			{Family: "feasibility", Title: "模型·训练·推理路径", Summary: "开源/厂商栈与延迟约束", SourceHints: []string{"github", "docs"}, Required: true},
			{Family: "precedents", Title: "基准与可复现对标", Summary: "SOTA、仓库、评测协议", SourceHints: []string{"benchmark", "arxiv"}, Required: true},
			{Family: "human_ai_boundary", Title: "人在回路与安全红线", Summary: "仅在目标涉及自动化责任时评估审批、评测和升级路径", SourceHints: []string{"case_study", "policy"}, Required: humanAIBoundaryRequired},
			{Family: "cost_schedule", Title: "训练/推理成本数量级", Summary: "算力与运维成本驱动", SourceHints: []string{"docs", "blog"}, Required: deliveryLike},
		}
	case researchDomainAcademicPapers:
		return []researchDimensionSeed{
			{Family: "problem_definition", Title: "研究问题与纳入标准", Summary: "范围、排除项、成功判据", SourceHints: []string{"arxiv", "journal"}, Required: true},
			{Family: "precedents", Title: "综述与里程碑文献", Summary: "方法族与引用链", SourceHints: []string{"arxiv", "journal"}, Required: true},
			{Family: "feasibility", Title: "方法局限与复现条件", Summary: "数据/算力/实验设计缺口", SourceHints: []string{"dataset", "github"}, Required: true},
			{Family: "risks", Title: "对立结论与争议", Summary: "冲突显式上图", SourceHints: []string{"arxiv", "lab_blog"}, Required: true},
			{Family: "human_ai_boundary", Title: "自动化检索与研究判断", Summary: "仅在目标涉及自动化研究流程时评估责任和审核边界", SourceHints: []string{"methodology"}, Required: humanAIBoundaryRequired},
			{Family: "open_questions", Title: "开放问题清单", Summary: "预算内未覆盖项", SourceHints: []string{"arxiv"}, Required: true},
		}
	case researchDomainFinance:
		return []researchDimensionSeed{
			{Family: "risks", Title: "监管与牌照约束", Summary: "能否对终端给「建议」等红线", SourceHints: []string{"regulator", "blog"}, Required: true},
			{Family: "resources", Title: "行情/另类数据可得性", Summary: "授权、延迟、成本", SourceHints: []string{"ir", "docs"}, Required: true},
			{Family: "feasibility", Title: "投研架构路径", Summary: "LLM vs 传统量化栈", SourceHints: []string{"research_note", "github"}, Required: true},
			{Family: "human_ai_boundary", Title: "签字与适当性回路", Summary: "自动化触达决策时评估签字、适当性和升级责任", SourceHints: []string{"regulator", "ir"}, Required: humanAIBoundaryRequired},
			{Family: "precedents", Title: "同类产品对标", Summary: "终端/Agent 能力边界", SourceHints: []string{"product", "news"}, Required: true},
			{Family: "cost_schedule", Title: "数据订阅 + 合规人力成本", Summary: "主要成本驱动", SourceHints: []string{"news", "ir"}, Required: deliveryLike},
		}
	case researchDomainDesignVisual:
		return []researchDimensionSeed{
			{Family: "problem_definition", Title: "受众与使用场景", Summary: "成功标准与非目标", SourceHints: []string{"case_study"}, Required: true},
			{Family: "precedents", Title: "风格参照与作品集", Summary: "可执行视觉规格前的参照集", SourceHints: []string{"portfolio", "x"}, Required: true},
			{Family: "feasibility", Title: "工具链与产出规格", Summary: "交付格式、协作流程", SourceHints: []string{"docs", "github"}, Required: true},
			{Family: "resources", Title: "素材与授权", Summary: "字体/素材版权风险", SourceHints: []string{"marketplace", "docs"}, Required: deliveryLike},
			{Family: "human_ai_boundary", Title: "人机创作边界", Summary: "仅在目标涉及生成式工作流时评估创作、授权和终审责任", SourceHints: []string{"case_study", "policy"}, Required: humanAIBoundaryRequired},
			{Family: "cost_schedule", Title: "制作与外包成本", Summary: "周期与成本驱动", SourceHints: []string{"marketplace", "case_study"}, Required: deliveryLike},
		}
	default:
		return []researchDimensionSeed{
			{Family: "problem_definition", Title: "问题定义与成功标准", Summary: "要解决什么、非目标、验收", SourceHints: []string{"web", "docs"}, Required: true},
			{Family: "requirements", Title: "需求与约束拆解", Summary: "功能/非功能/优先级", SourceHints: []string{"web", "docs"}, Required: deliveryLike},
			{Family: "feasibility", Title: "可行性与路径", Summary: "技术/组织条件缺口", SourceHints: []string{"github", "docs"}, Required: true},
			{Family: "precedents", Title: "对标与先例", Summary: "有没有人做过、长什么样", SourceHints: []string{"github", "x"}, Required: true},
			{Family: "human_ai_boundary", Title: "人机责任边界", Summary: "仅在目标涉及自动化分工时评估审批、责任和升级路径", SourceHints: []string{"case_study", "policy"}, Required: humanAIBoundaryRequired},
			{Family: "cost_schedule", Title: "成本与周期", Summary: "粗算区间与成本驱动", SourceHints: []string{"web"}, Required: deliveryLike},
		}
	}
}

func researchFineDomainPlaybook(fine string) string {
	books := researchDomainPlaybooks()
	if md := books[fine]; md != "" {
		return md
	}
	return books["general"]
}

func mergeSourceWhyPayload(existing json.RawMessage, why, dimensionFamily string) json.RawMessage {
	obj := map[string]any{}
	if len(existing) > 0 {
		_ = json.Unmarshal(existing, &obj)
	}
	if obj == nil {
		obj = map[string]any{}
	}
	why = strings.TrimSpace(why)
	if why != "" {
		obj["why"] = why
	}
	if strings.TrimSpace(dimensionFamily) != "" {
		obj["dimension_family"] = strings.TrimSpace(dimensionFamily)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return existing
	}
	return raw
}

func sourceWhyFromPayload(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return ""
	}
	if v, ok := obj["why"].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func reportHasHumanAIBoundary(content string) bool {
	c := strings.ToLower(content)
	needles := []string{
		"ai 上限", "仅靠 ai", "必须有人", "缺人", "人做", "ai 做", "人机",
		"human", "ai-only", "must have human", "human vs ai",
	}
	hits := 0
	for _, n := range needles {
		if strings.Contains(c, n) {
			hits++
		}
	}
	return hits >= 2
}

func adaptiveKickoffLeadPrompt(goal string, plan researchAdaptivePlan) string {
	var b strings.Builder
	b.WriteString("Legacy exploration session ready on the research canvas. This canvas is a compatibility path; do not treat its stage labels or playbook as the research method.\n")
	b.WriteString(fmt.Sprintf("Goal: %s\n", goal))
	b.WriteString(fmt.Sprintf("Tentative domain profile: %s (related profiles: %s). Treat this classification as a hypothesis and replace it when the goal or evidence disagrees.\n", plan.FineDomain, strings.Join(plan.CoarseDomains, ",")))
	b.WriteString("Research rules:\n")
	b.WriteString("- State the decision question, scope, method, evidence needs, counter-tests, and stopping rule before collecting broadly.\n")
	b.WriteString("- Expand only decision-relevant uncertainties. Do not use a fixed question checklist, fixed branch count, fixed source count, or universal source hierarchy.\n")
	b.WriteString("- For each material Claim, record what evidence traits can establish it, why the source fits, what would disconfirm it, and what remains uncertain.\n")
	b.WriteString("- Hire specialists via multica research hire only for a real specialty gap (required reason); soft roster cap 12. After activate, members must work. Capacity/409 fixtures use --fixture (no canvas pad walls). Only you may change roster.\n")
	b.WriteString("- Do NOT rewrite the user's session goal; user mid-flight only (LRM-898).\n")
	b.WriteString("- Select source channels from the Claim and method. Every source-upsert MUST include payload.why explaining which Claim or uncertainty the source can resolve.\n")
	if plan.HumanAIBoundaryRequired {
		b.WriteString("- The user goal explicitly requires a human/AI responsibility analysis; report approval, escalation, and accountability boundaries.\n")
	} else {
		b.WriteString("- Do not add a human/AI section unless it changes the requested decision.\n")
	}
	b.WriteString("- Candidate dimensions; refine, merge, remove, or replace them from the goal and evidence:\n")
	for _, d := range plan.Dimensions {
		req := ""
		if d.Required {
			req = " [required]"
		}
		b.WriteString(fmt.Sprintf("  - %s (%s)%s — hints: %s\n", d.Title, d.Family, req, strings.Join(d.SourceHints, "/")))
	}
	b.WriteString("\nMethod adaptation profile excerpt:\n")
	excerpt := plan.PlaybookMD
	if len([]rune(excerpt)) > 1200 {
		excerpt = string([]rune(excerpt)[:1200]) + "…"
	}
	b.WriteString(excerpt)
	b.WriteString("\n\nRecord a concise method decision, then dispatch only evidence-producing probes via multica research graph-append / message --target / source-upsert (with --why). Observe results, evaluate gaps and contradictions, and revise the plan when evidence invalidates it. Keep the user updated through 罗纳尔多 voice only.\n")
	return b.String()
}
