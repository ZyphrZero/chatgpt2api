package service

import "testing"

func TestHtmlInAnnouncementDetectsLegacyMarkup(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want bool
	}{
		{"plain markdown heading", "# 公告\n请尽快更新", false},
		{"plain markdown list", "- 第一条\n- 第二条", false},
		{"empty", "", false},
		{"html paragraph", "<p>稍后维护</p>", true},
		{"html strong", "<strong>重点</strong>", true},
		{"html link", `<a href="https://x.test">x</a>`, true},
		{"html br", "维护通知<br>感谢配合", true},
		{"html list", "<ul><li>a</li><li>b</li></ul>", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := htmlInAnnouncement(tc.in); got != tc.want {
				t.Fatalf("htmlInAnnouncement(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestAnnouncementHTMLToMarkdownConvertsCommonTags(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "heading and paragraph",
			in:   "<h3>更新公告</h3><p>支持加粗、链接、列表等富文本。</p>",
			want: "### 更新公告\n\n支持加粗、链接、列表等富文本。",
		},
		{
			name: "strong and em",
			in:   "<p><strong>重点</strong>内容 <em>提醒</em></p>",
			want: "**重点**内容 *提醒*",
		},
		{
			name: "anchor with href",
			in:   `<p>详情：<a href="https://images.dfmcn.com/">点击</a></p>`,
			want: "详情：[点击](https://images.dfmcn.com/)",
		},
		{
			name: "ul list",
			in:   "<ul><li>第一条</li><li>第二条</li></ul>",
			want: "- 第一条\n- 第二条",
		},
		{
			name: "ol list",
			in:   "<ol><li>登录</li><li>选择模型</li></ol>",
			want: "1. 登录\n2. 选择模型",
		},
		{
			name: "blockquote",
			in:   "<blockquote>请勿外传</blockquote>",
			want: "> 请勿外传",
		},
		{
			name: "br between sentences",
			in:   "维护通知<br>预计恢复 09:00",
			want: "维护通知\n预计恢复 09:00",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := announcementHTMLToMarkdown(tc.in)
			if got != tc.want {
				t.Fatalf("announcementHTMLToMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMigrateAnnouncementsToMarkdownIsIdempotent(t *testing.T) {
	items := []map[string]any{
		{"id": "a", "content": "<p>维护中</p>"},
		{"id": "b", "content": "# 已经是 Markdown\n保留原样"},
		{"id": "c", "content": ""},
	}
	if !migrateAnnouncementsToMarkdown(items) {
		t.Fatal("first pass should report change=true")
	}
	if items[0]["content"] != "维护中" {
		t.Fatalf("legacy HTML not migrated: %#v", items[0])
	}
	if items[1]["content"] != "# 已经是 Markdown\n保留原样" {
		t.Fatalf("plain markdown should be untouched: %#v", items[1])
	}
	if migrateAnnouncementsToMarkdown(items) {
		t.Fatal("second pass should be a no-op")
	}
}
