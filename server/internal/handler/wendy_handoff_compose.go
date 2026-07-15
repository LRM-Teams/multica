package handler

import (
	"context"
	"fmt"
)

type UnlockComposeInput struct {
	TargetAgentID   string
	TargetAgentName string
	IssueTitle      string
}

type WendyComposer interface {
	ComposeUnlock(context.Context, UnlockComposeInput) (string, error)
}

type templateWendyComposer struct{}

func (templateWendyComposer) ComposeUnlock(_ context.Context, in UnlockComposeInput) (string, error) {
	return fmt.Sprintf(
		"%s 前置已完成，请开始处理：%s",
		mentionMarkdown("agent", in.TargetAgentID, in.TargetAgentName),
		in.IssueTitle,
	), nil
}

func mentionMarkdown(mentionType, id, name string) string {
	_ = mentionType
	_ = id
	return directedAgentMentionLabel(name)
}
