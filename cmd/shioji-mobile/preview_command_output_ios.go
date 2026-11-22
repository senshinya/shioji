package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"shioji/internal/domain"
)

const previewCommandItem = "streaming-command"

func (a *mobileApp) seedPreviewCommandOutput(id string) {
	a.seedArtboardConversation(false)
	a.model.Preferences.CollapseLatestTools = false
	detail := a.model.Details[id]
	detail.Session.Status = domain.ExecutionRunning
	detail.Session.Title = "核对命令输出"
	started := time.Now().Add(-4 * time.Second)
	detail.Turns = []domain.Turn{{ID: "terminal-turn", Status: domain.ExecutionRunning, StartedAt: &started}}
	detail.Queue.Items, detail.Works = nil, nil
	lines := []string{"ORIGINAL-LOG-START", "early-anchor provisioning"}
	for i := 3; i <= 51; i++ {
		lines = append(lines, fmt.Sprintf("Step %02d. Validated a journal entry.", i))
	}
	lines = append(lines, "STREAM-TAIL-52")
	detail.Items = []domain.Item{
		{ID: "terminal-prompt", Kind: "user", TurnID: "terminal-turn", Text: "请核对完整的连接日志。", Order: 1},
		{ID: previewCommandItem, Kind: "tool", Presentation: "command", TurnID: "terminal-turn", Name: "核对归档清理", Status: "running",
			Text: strings.Join(lines, "\n") + "\n", Order: 2, ActivityOrder: 1},
	}
	a.model.Details[id] = detail
	a.model.Snapshot.Sessions[0] = detail.Session
	a.model.Drafts["session:"+id] = "保留这条日志检查草稿"
}

func (a *mobileApp) previewCommandOpened(sid, id string) {
	if !a.preview || id != previewCommandItem {
		return
	}
	time.AfterFunc(5*time.Second, func() {
		mygo.RunOnMain(func() {
			d := a.model.Details[sid]
			if len(d.Items) != 2 || d.Items[1].ID != id || d.Items[1].Final {
				return
			}
			d.Items[1].Text += "Final phase started.\nDrained the recovery journal.\nFINAL-TAIL-55\n"
			d.Items[1].Final, d.Items[1].Status = true, "completed"
			a.model.Details[sid] = d
			a.refresh()
		})
	})
}
