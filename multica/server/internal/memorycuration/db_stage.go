package memorycuration

func DBStageName(stage Stage) string {
	switch stage {
	case StageAgentSelfReview:
		return "agent_self_review"
	case StageTeamCuration:
		return "team_curation"
	case StageL1:
		return "l1_daily"
	case StageL2:
		return "l2_review"
	case StageL3:
		return "l3_promote"
	case StageL4:
		return "l4_curator"
	default:
		return "all"
	}
}
