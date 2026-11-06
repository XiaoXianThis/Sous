package node

import "strings"

func (r *run) failureStatus() string {
	if r.ctx.Err() != nil {
		return "cancelled"
	}
	return "failed"
}

// prBody is the body of a pull request sous opens when the user asks for one.
func (r *run) prBody(commits string) string {
	var b strings.Builder
	if p := strings.TrimSpace(r.spec.Prompt); p != "" {
		b.WriteString(p + "\n\n")
	}
	b.WriteString("## Commits\n\n```\n" + commits + "\n```\n\n")
	b.WriteString("_由 [sous](https://github.com/XiaoXianThis/Sous) 创建。_\n")
	return b.String()
}
