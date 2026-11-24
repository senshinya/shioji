package domain

import "strings"

const DefaultHistoryRounds = 20
const MaxHistoryRounds = 100

// HistoryQuery addresses device-owned display history, never harness context.
// Before and After are exclusive; AroundID starts a window at a stable item.
type HistoryQuery struct {
	Generation  string `json:"generation,omitempty"`
	SessionID   string `json:"sessionId"`
	ThreadID    string `json:"threadId,omitempty"`
	Before      int64  `json:"before,omitempty"`
	After       int64  `json:"after,omitempty"`
	AroundID    string `json:"aroundId,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Rounds      int    `json:"rounds,omitempty"`
	WindowFirst int64  `json:"windowFirst,omitempty"`
	WindowLast  int64  `json:"windowLast,omitempty"`
	SummaryOnly bool   `json:"summaryOnly,omitempty"`
}

type HistoryWindow struct {
	LiveSequence int64              `json:"-"`
	WindowFirst  int64              `json:"windowFirst,omitempty"`
	WindowLast   int64              `json:"windowLast,omitempty"`
	SummaryDirty int64              `json:"-"`
	First        int64              `json:"first"`
	Last         int64              `json:"last"`
	Older        bool               `json:"older"`
	Newer        bool               `json:"newer"`
	ContextID    string             `json:"contextId,omitempty"`
	Invalidated  bool               `json:"invalidated,omitempty"`
	Groups       []HistoryToolGroup `json:"groups,omitempty"`
}

type HistoryToolGroup struct {
	ID     string `json:"id"`
	TurnID string `json:"turnId,omitempty"`
	First  int64  `json:"first"`
	Last   int64  `json:"last"`
	Count  int    `json:"count"`
	Issues int    `json:"issues"`
}

type HistoryPrompt struct {
	ID     string `json:"id"`
	TurnID string `json:"turnId,omitempty"`
	Order  int64  `json:"order"`
	Text   string `json:"text"`
}

type HistoryOutline struct {
	SessionID string          `json:"sessionId"`
	ThreadID  string          `json:"threadId,omitempty"`
	Prompts   []HistoryPrompt `json:"prompts"`
	Older     bool            `json:"older"`
	Sequence  int64           `json:"sequence"`
}

type HistorySearchQuery struct {
	HistoryQuery
	Query string `json:"query"`
}
type HistoryMatch struct {
	ID    string `json:"id"`
	Order int64  `json:"order"`
	Count int    `json:"count"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}
type HistoryMatches struct {
	Revision  int64          `json:"revision"`
	SessionID string         `json:"sessionId"`
	ThreadID  string         `json:"threadId,omitempty"`
	Query     string         `json:"query"`
	Matches   []HistoryMatch `json:"matches"`
	More      bool           `json:"more"`
	Sequence  int64          `json:"sequence"`
}

// ReasoningSummary identifies the public reasoning rows in conversation history.
func ReasoningSummary(item Item) bool {
	return item.Presentation == "reasoning" && (item.Kind == "activity" || item.Kind == "reasoning")
}

// ToolGroupMember identifies items that can appear inside an active tool group:
// tool calls and subsequent public reasoning summaries. A tool group starts on
// a tool call and never pulls earlier reasoning summaries backward into it.
// Spacers can occur between members without becoming visible rows or adding to
// the call count.
func ToolGroupMember(item Item) bool {
	return item.Kind == "tool" || ReasoningSummary(item)
}

const AutomaticApprovalPresentation = "automatic-approval"

// HistorySpacer reports invisible records that do not split a tool group.
func HistorySpacer(item Item) bool {
	return item.Kind == "assistant" && strings.TrimSpace(item.Text) == "" && len(item.Questions) == 0 && len(item.Attachments) == 0 && len(item.Parts) == 0 ||
		ReasoningSummary(item) && emptyReasoningSummary(item) ||
		item.Kind == "request_result" && len(item.Parts) == 0 && item.WorkID == "" ||
		item.Kind == "request" && item.Presentation == AutomaticApprovalPresentation ||
		InlineRequestResultAnchor(item) != ""
}

func emptyReasoningSummary(item Item) bool {
	if item.Truncated || strings.TrimSpace(item.Text) != "" || len(item.Files) != 0 || len(item.Agents) != 0 {
		return false
	}
	for _, part := range item.Parts {
		if part.Attachment != nil || strings.TrimSpace(part.Text) != "" {
			return false
		}
	}
	return true
}

func HasInlineRequestReceipt(kind string) bool {
	return kind == "questions" || kind == "approval" || kind == "permissions"
}

// InlineRequestResultAnchor links a journal receipt to its original history row.
func InlineRequestResultAnchor(item Item) string {
	if item.Kind == "request_result" && HasInlineRequestReceipt(item.Presentation) {
		if id, ok := strings.CutPrefix(item.ID, "request-result:"); ok && id != "" {
			return "request:" + id
		}
	}
	return ""
}
