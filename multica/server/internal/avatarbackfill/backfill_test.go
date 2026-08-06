package avatarbackfill

import (
	"strings"
	"testing"
)

func TestRewriteUploadsPath(t *testing.T) {
	t.Parallel()
	base := "https://leagent.s3.oss-cn-beijing.aliyuncs.com"
	got, ok := RewriteUploadsPath(base, "/uploads/workspaces/ws/abc.png")
	if !ok {
		t.Fatal("expected ok")
	}
	want := "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/ws/abc.png"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, ok := RewriteUploadsPath(base, "https://cdn.example/avatar.png"); ok {
		t.Fatal("absolute URL must not rewrite")
	}
	if _, ok := RewriteUploadsPath(base, "/uploads/"); ok {
		t.Fatal("empty key must not rewrite")
	}
}

func TestResolveAvatarURLPrefersMigratedAttachment(t *testing.T) {
	t.Parallel()
	base := DefaultPublicBaseURL
	stale := "/uploads/workspaces/ws/agent.png"
	migrated := "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/ws/agent.png"

	got := ResolveAvatarURL(base, stale, migrated)
	if got != migrated {
		t.Fatalf("expected attachment URL, got %q", got)
	}

	// Attachment still local → fall back to path rewrite.
	got = ResolveAvatarURL(base, stale, "/uploads/workspaces/ws/agent.png")
	if got != migrated {
		t.Fatalf("expected rewritten URL, got %q", got)
	}

	// Already good denormalized URL stays put.
	good := "https://cdn.example/a.png"
	if got := ResolveAvatarURL(base, good, migrated); got != good {
		t.Fatalf("expected unchanged %q, got %q", good, got)
	}
}

func TestResolveAvatarURLMatchesCascadeSemantics(t *testing.T) {
	t.Parallel()
	// Mirrors: avatar_source=uploaded prefers attachment.url when attachment
	// left /uploads/; otherwise rewrite keeps denormalized field consistent
	// with the object key under public base.
	cases := []struct {
		name       string
		avatar     string
		attachment string
		want       string
	}{
		{
			name:       "agent uploaded after attachment migrate",
			avatar:     "/uploads/workspaces/7bea/019f.png",
			attachment: "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/7bea/019f.png",
			want:       "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/7bea/019f.png",
		},
		{
			name:       "user without attachment_id",
			avatar:     "/uploads/workspaces/7bea/user.png",
			attachment: "",
			want:       "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/7bea/user.png",
		},
		{
			name:       "channel avatar",
			avatar:     "/uploads/workspaces/7bea/ch.png",
			attachment: "",
			want:       "https://leagent.s3.oss-cn-beijing.aliyuncs.com/workspaces/7bea/ch.png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ResolveAvatarURL(DefaultPublicBaseURL, tc.avatar, tc.attachment)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if strings.HasPrefix(got, "/uploads/") {
				t.Fatalf("resolved URL still on /uploads/: %q", got)
			}
		})
	}
}
