package mobileclient

import (
	"slices"

	"shioji/internal/domain"
)

func ContextCompactionLabel(state domain.ExecutionState) string {
	switch state {
	case domain.ExecutionCompleted:
		return "上下文已压缩"
	case domain.ExecutionFailed:
		return "上下文压缩失败"
	case domain.ExecutionInterrupted:
		return "上下文压缩已停止"
	case domain.ExecutionUnknown:
		return "压缩状态待核对"
	default:
		if state.Active() {
			return "正在压缩上下文"
		}
		return ""
	}
}

// CompactionItems bridges local submission and the journal. localAfter points
// to the last observed item order when the current command was submitted.
func CompactionItems(items []domain.Item, action *domain.ContextAction, localAfter *int64, atLatest bool) []domain.Item {
	if action == nil || action.OperationID == "" {
		return items
	}
	commandID := "context-command:" + action.OperationID
	commandIndex, activityIndex := -1, -1
	for i, item := range items {
		if item.ID == commandID {
			commandIndex = i
		}
		if action.TurnID != "" && item.TurnID == action.TurnID && item.Presentation == "compaction" {
			activityIndex = i
		}
	}
	label := ContextCompactionLabel(action.State)
	if activityIndex >= 0 && label != "" && !action.State.Active() {
		items = slices.Clone(items)
		item := &items[activityIndex]
		item.Final, item.Name, item.Status = true, label, string(action.State)
	}
	if !atLatest {
		return items
	}
	if commandIndex < 0 && localAfter != nil {
		index := len(items)
		for i, item := range items {
			if (action.TurnID != "" && item.TurnID == action.TurnID) || item.Order > *localAfter {
				index = i
				break
			}
		}
		items = slices.Insert(slices.Clone(items), index, domain.Item{ID: commandID, Kind: "command", Presentation: "compact",
			TurnID: action.TurnID, Name: "压缩上下文", Final: true})
		commandIndex = index
	}
	// A completed action can remain on a session after its command and
	// result have both left the loaded window.
	if activityIndex >= 0 || label == "" || action.State == domain.ExecutionUnknown || (!action.State.Active() && commandIndex < 0) {
		return items
	}
	index := len(items)
	if commandIndex >= 0 {
		index = commandIndex + 1
	}
	return slices.Insert(slices.Clone(items), index, domain.Item{ID: "context-compaction:" + action.OperationID, TurnID: action.TurnID, Kind: "activity",
		Presentation: "compaction", Name: label, Status: string(action.State), Final: !action.State.Active()})
}

// LiveCompaction only reports the newest relevant, unfinished activity in
// the active turn. Late activity from an earlier turn cannot reclaim it.
func LiveCompaction(items []domain.Item, turns []domain.Turn) string {
	for _, turn := range slices.Backward(turns) {
		if !turn.Status.Active() {
			continue
		}
		var latest *domain.Item
		for _, item := range slices.Backward(items) {
			if item.TurnID != turn.ID || item.WorkID != "" {
				continue
			}
			eligible := item.Kind == "tool" || item.Kind == "assistant" && (item.ActivityOrder > 0 || item.Text != "") ||
				item.Kind == "reasoning" || item.Presentation == "reasoning" || item.Presentation == "compaction"
			if eligible && (latest == nil || item.ActivityOrder > latest.ActivityOrder) {
				latest = &item
			}
		}
		if latest != nil && latest.Presentation == "compaction" && !latest.Final {
			return latest.ID
		}
		break
	}
	return ""
}
