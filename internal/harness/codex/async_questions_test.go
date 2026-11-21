package codex

import (
	"encoding/json"
	"testing"

	"shioji/internal/domain"
)

func TestAsyncCodexQuestionRetainsChoicesWithoutOpeningRequest(t *testing.T) {
	n := NewNormalizer()
	n.Bind("thread", "session", true)
	events, reply, err := n.Feed([]byte(`{"method":"item/completed","params":{"threadId":"thread","turnId":"turn","item":{"id":"call","type":"agentMessage","delivery":"async","phase":"final_answer","text":"When?\n- After locking\n- While awake","questions":[{"title":"When?","options":["After locking","While awake"]},{"title":"Which device?"}]}}}`))
	if err != nil || len(reply) != 0 {
		t.Fatalf("async notification attempted an RPC reply: %s, %v", reply, err)
	}
	for _, e := range events {
		if e.Request != nil {
			t.Fatal("async question opened a blocking request")
		}
	}
	if len(events) != 1 || events[0].Item == nil || !events[0].Item.Final {
		t.Fatalf("missing completed conversation item: %+v", events)
	}
	body, err := json.Marshal(events[0].Item)
	if err != nil {
		t.Fatal(err)
	}
	var item struct {
		Text      string
		Questions []domain.Question
	}
	if err := json.Unmarshal(body, &item); err != nil {
		t.Fatal(err)
	}
	if item.Text != "When?\n- After locking\n- While awake" || len(item.Questions) != 2 {
		t.Fatalf("lost structured questions alongside the fallback: %s", body)
	}
	first, second := item.Questions[0], item.Questions[1]
	if first.ID != `["request_user_input_async","call",0]` || !first.Custom || first.Text != "When?" ||
		len(first.Options) != 2 || first.Options[1].Label != "While awake" ||
		second.ID != `["request_user_input_async","call",1]` || !second.Custom || len(second.Options) != 0 {
		t.Fatalf("questions: %+v", item.Questions)
	}
}

func TestAsyncQuestionReplyUsesCodexUserInputEnvelope(t *testing.T) {
	const questionID = `["request_user_input_async","call",0]`
	reply := asyncQuestionInput("Both awake", &domain.QuestionReply{QuestionID: questionID, Title: "When?\nAfter locking?"})
	const want = "<send_user_message_question_reply>\n" +
		`[{"questionItemId":"[\"request_user_input_async\",\"call\",0]","question":"When? After locking?","answer":"Both awake"}]` +
		"\n</send_user_message_question_reply>"
	if reply != want {
		t.Fatalf("unexpected user input: %s", reply)
	}
}
