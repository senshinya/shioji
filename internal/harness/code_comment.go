package harness

import (
	"fmt"
	"strings"

	"shioji/internal/domain"
)

// FormatCodeComment supplies the selected diff context to a text-based adapter.
func FormatCodeComment(comment domain.CodeCommentContent, branch string) string {
	endpoint := func(e domain.DiffEndpoint) string {
		if e.Kind == "empty" {
			return "空版本"
		}
		if e.Kind == "worktree" {
			return "工作目录 · " + e.Branch + "，基于 " + e.Commit
		}
		name := e.Name
		if e.Kind == "head" && e.Branch != "" {
			name = e.Branch + " · HEAD"
		}
		return name + " @ " + e.Commit
	}
	var out strings.Builder
	fmt.Fprintf(&out, "代码评论\n比较基准：%s\n比较目标：%s\n文件：%s\n", endpoint(comment.Comparison.Base), endpoint(comment.Comparison.Target), comment.Path)
	if comment.FileSnapshot != "" {
		fmt.Fprintf(&out, "文件快照：%s\n", comment.FileSnapshot)
	}
	for _, part := range comment.Fragments {
		side := "目标侧"
		if part.Side == "base" {
			side = "基准侧"
		}
		endLine := part.StartLine + strings.Count(part.Text, "\n")
		fmt.Fprintf(&out, "选中侧：%s\n位置：%s:%d:%d–%d:%d（末端不含）\n选中文字：\n%s\n", side, comment.Path, part.StartLine, part.StartColumn, endLine, part.EndColumn, part.Text)
	}
	fmt.Fprintf(&out, "评论：%s\n会话工作目录分支：%s", comment.Comment, branch)
	return out.String()
}
