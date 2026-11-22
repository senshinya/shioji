package worker

import "testing"

func TestAsyncQuestionCommandValidatesInputBeforeDispatch(t *testing.T) {
	server, _ := memoryWorker(t, memoryHarness{id: "fake"})
	if _, err := server.handle("question.reply", []byte("{}")); err == nil || err.Error() != "请输入有效的答案" {
		t.Fatalf("question reply command must validate missing input: %v", err)
	}
}
