package codex

import "testing"

func TestBackgroundClassificationFollowsItsOwningTurn(t *testing.T) {
	for _, child := range []bool{false, true} {
		name := "root"
		if child {
			name = "child"
		}
		t.Run(name, func(t *testing.T) {
			n := NewNormalizer()
			n.Bind("root", "s", false)
			thread := "root"
			if child {
				workFeed(t, n, `{"method":"item/completed","params":{"threadId":"root","turnId":"rt","item":{"type":"collabAgentToolCall","id":"spawn","tool":"spawnAgent","receiverThreadIds":["child"],"agentsStates":{"child":{"status":"running"}}}}}`)
				thread = "child"
			}
			workFeed(t, n, `{"method":"turn/started","params":{"threadId":"`+thread+`","turn":{"id":"owned-turn","status":"inProgress"}}}`)
			workFeed(t, n, `{"method":"item/started","params":{"threadId":"`+thread+`","turnId":"owned-turn","item":{"type":"commandExecution","id":"cmd","command":"long server","processId":"p","status":"inProgress"}}}`)
			workFeed(t, n, `{"method":"item/started","params":{"threadId":"`+thread+`","turnId":"owned-turn","item":{"type":"agentMessage","id":"next","text":"continuing"}}}`)
			key := thread + "\x00owned-turn/cmd"
			if cmd := n.workState().commands[key]; cmd == nil || !cmd.promoted || cmd.work.State != "running" {
				t.Fatal("active owner lost running classification", cmd)
			}
			if child {
				workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"root","turn":{"id":"rt","status":"completed"}}}`)
				if cmd := n.workState().commands[key]; cmd.work.State != "running" {
					t.Fatal("parent completion demoted active child command", cmd.work)
				}
			}
			workFeed(t, n, `{"method":"turn/completed","params":{"threadId":"`+thread+`","turn":{"id":"owned-turn","status":"completed"}}}`)
			if cmd := n.workState().commands[key]; cmd.work.State != "waiting" {
				t.Fatal("terminal owner background never became waiting", cmd.work)
			}
			workFeed(t, n, `{"method":"item/commandExecution/outputDelta","params":{"threadId":"`+thread+`","turnId":"owned-turn","itemId":"cmd","delta":"late output"}}`)
			if cmd := n.workState().commands[key]; cmd.work.State != "waiting" {
				t.Fatal("late output revived native foreground work", cmd.work)
			}
		})
	}
}
