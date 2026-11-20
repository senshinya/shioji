package domain

import (
	"errors"
	"strings"
	"unicode"
)

type GoalState string

const (
	GoalActive        GoalState = "active"
	GoalPaused        GoalState = "paused"
	GoalBlocked       GoalState = "blocked"
	GoalUsageLimited  GoalState = "usageLimited"
	GoalBudgetLimited GoalState = "budgetLimited"
	GoalComplete      GoalState = "complete"
)

type Goal struct {
	Objective      string    `json:"objective"`
	State          GoalState `json:"state"`
	ElapsedSeconds int64     `json:"elapsedSeconds"`
	CreatedAt      int64     `json:"createdAt"`
}

func (g *Goal) Active() bool { return g != nil && g.State == GoalActive }

func (g *Goal) CanResume() bool {
	return g != nil && (g.State == GoalPaused || g.State == GoalBlocked || g.State == GoalUsageLimited)
}

// SameTarget tolerates progress updates while preventing an action intended
// for one objective or state from mutating a replacement.
func (g *Goal) SameTarget(other *Goal) bool {
	if g == nil || other == nil {
		return g == other
	}
	return g.Objective == other.Objective && g.CreatedAt == other.CreatedAt && g.State == other.State
}

type GoalAction string

const (
	SetGoal    GoalAction = "set"
	PauseGoal  GoalAction = "pause"
	ResumeGoal GoalAction = "resume"
	ClearGoal  GoalAction = "clear"
)

type GoalChange struct {
	Action    GoalAction `json:"action"`
	Objective string     `json:"objective,omitempty"`
}

type GoalRequest struct {
	OperationID string     `json:"operationId"`
	SessionID   string     `json:"sessionId"`
	Target      Goal       `json:"target"`
	Action      GoalAction `json:"action"`
}

const MaxGoalRunes = 4000

const GoalContinuationPresentation = "goal-continuation"

func GoalCommand(text string) (objective string, command bool) {
	text = strings.TrimLeftFunc(text, unicode.IsSpace)
	end := strings.IndexFunc(text, unicode.IsSpace)
	if end < 0 {
		return "", text == "/goal"
	}
	return strings.TrimSpace(text[end:]), text[:end] == "/goal"
}

func (in Submission) ValidateGoal() error {
	if in.GoalObjective == "" {
		return nil
	}
	objective, command := GoalCommand(in.Text)
	if !command || objective != in.GoalObjective || len([]rune(objective)) > MaxGoalRunes ||
		len(in.Attachments) != 0 || len(in.Skills) != 0 || len(in.ReferencePositions) != 0 || in.Continue || in.Config.Plan {
		return errors.New("目标需要独立的 /goal 正文，并使用执行模式")
	}
	return nil
}
