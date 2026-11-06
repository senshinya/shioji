package worker_test

import (
	"context"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/worker"
	"testing"
	"time"
)

func TestControllerHydratesBothVisibleConversationsAfterReconnect(t *testing.T) {
	server, client, _ := startServer(t)
	var w domain.Workspace
	call(t, client, "workspace.create", worker.WorkspaceInput{Name: "Parallel", Folders: []string{t.TempDir()}}, &w)
	var ready domain.Readiness
	call(t, client, "readiness", nil, &ready)
	ids := []string{}
	for _, id := range []string{"left", "right"} {
		var op domain.Operation
		call(t, client, "session.submit", domain.Submission{OperationID: id, WorkspaceID: w.ID, Text: id, Config: domain.RunConfig{Model: ready.Models[0].ID, Permission: "read-only"}}, &op)
		ids = append(ids, op.SessionID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controller := app.NewController(server.Directory())
	go controller.Run(ctx)
	m := app.NewModel()
	controller.Do(app.Job{Method: "$visible", Input: ids})
	drain := func() {
		deadline := time.After(4 * time.Second)
		for !(m.Connected && m.Details[ids[0]].Session.ID == ids[0] && m.Details[ids[1]].Session.ID == ids[1]) {
			select {
			case update := <-controller.Updates:
				update(m)
			case <-deadline:
				t.Fatal("both visible conversations were not hydrated")
			}
		}
	}
	drain()
	m.Details = map[string]domain.SessionDetail{}
	controller.Do(app.Job{Method: "$visible", Input: []string{}})
	controller.Do(app.Job{Method: "$visible", Input: ids})
	drain()
	m.Details = map[string]domain.SessionDetail{}
	controller.Do(app.Job{Method: "$reconnect"})
	drain()
}
