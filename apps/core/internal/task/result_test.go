package task

import (
	"strings"
	"testing"
)

func TestParseResult(t *testing.T) {
	summary := "我先看了代码，然后改了登录页。\n\n## 结果\n- 做了：修好了登录页的跳转\n- 改动：web/src/Login.tsx\n" +
		"- 验证：跑了 npm test，全部通过\n  另外手动点了一遍\n- 没做完 / 风险：无\n- 需要决定：要不要顺便删掉旧的 /signin 路由\n"
	r, ok := ParseResult(summary)
	if !ok {
		t.Fatal("no result section found")
	}
	want := Result{Done: "修好了登录页的跳转", Changed: "web/src/Login.tsx", Verified: "跑了 npm test，全部通过 另外手动点了一遍",
		Left: "无", Decide: "要不要顺便删掉旧的 /signin 路由"}
	if r != want {
		t.Fatalf("got %+v\nwant %+v", r, want)
	}
	text := r.Text()
	if strings.Contains(text, "没做完") || !strings.Contains(text, "需要决定：要不要") {
		t.Fatalf("text: %q", text)
	}
}

func TestParseResultVariants(t *testing.T) {
	for name, summary := range map[string]string{
		"bold":      "**结果**\n* **做了**：部署了 v2\n* **验证**: curl 返回 200\n",
		"heading 3": "### 结果：\n- 做了: 部署了 v2\n- 验证：curl 返回 200\n\n## 其他\n- 做了：不该算进去",
		"last wins": "## 结果\n- 做了：草稿\n\n后来又改了。\n## 结果\n- 做了：部署了 v2\n- 验证：curl 返回 200",
	} {
		r, ok := ParseResult(summary)
		if !ok || r.Done != "部署了 v2" || r.Verified != "curl 返回 200" {
			t.Errorf("%s: %+v %v", name, r, ok)
		}
	}
	if _, ok := ParseResult("改好了，测试通过。结果很好。"); ok {
		t.Error("prose that mentions 结果 is not a section")
	}
	if got := summaryText("没有结果小节，只是一段话", 5); got != "没有结果小…" {
		t.Errorf("fallback: %q", got)
	}
}

func TestFileDiff(t *testing.T) {
	diff := "diff --git a/a.go b/a.go\n+one\ndiff --git a/dir/b.go b/dir/b.go\n+two\n"
	if got := fileDiff(diff, "dir/b.go"); got != "diff --git a/dir/b.go b/dir/b.go\n+two\n" {
		t.Fatalf("got %q", got)
	}
	if fileDiff(diff, "c.go") != "" {
		t.Fatal("a missing file has no diff")
	}
}
