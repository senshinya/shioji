package ui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/domain"
	"shioji/internal/harness/codex"
)

const replyBurst = "First words. " + "这是一段逐步出现的中文回复。" + " The answer continues with more detail instead of appearing all at once. Stream tail."

func TestAdaptiveMarkdownResetsWhenUnrevealedTailIsReplaced(t *testing.T) {
	text := strings.Repeat("A", 1000)
	options := markdown.Options{Reveal: &markdown.Reveal{Smooth: true, Fade: -1}}
	ui := n.NewTester(func(c *n.Context) { markdown.Stream(c.Key("reply"), text, &options).FillWidth() }, 1100, 800)
	text = strings.Repeat("A", 100) + " changed tail"
	ui.Frame()
	if !ui.HasText(text) || ui.FramePending() {
		t.Fatal("replacing an unrevealed suffix retains the old stream backlog")
	}
}

// If each batch is drained immediately, a regular four-character/100 ms
// stream alternates rapid growth with visible pauses. Measure content growth,
// independently of whether the fade animation is still drawing frames.
func TestLiveReplyAbsorbsRegularInputBatches(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	source := []rune(strings.Repeat("甲乙丙丁戊己庚辛壬癸", 10))
	lastLength, lastGrowth := 0, time.Duration(0)
	var maxGap time.Duration
	for frame := 0; frame < 120; frame++ {
		elapsed := time.Duration(frame) * time.Second / 60
		if frame%6 == 0 {
			d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: string(source[:4*(frame/6+1)])}}
			m.Details["s"] = d
			ui.Frame()
		}
		ui.Advance(time.Second / 60)
		length := 0
		for _, text := range ui.Texts() {
			if strings.HasPrefix(text, "甲") {
				length = len([]rune(text))
				break
			}
		}
		if length != lastLength {
			if elapsed > 400*time.Millisecond {
				maxGap = max(maxGap, elapsed-lastGrowth)
			}
			lastLength, lastGrowth = length, elapsed
		}
	}
	t.Logf("maximum content-growth gap: %s", maxGap)
	if maxGap > 50*time.Millisecond {
		t.Fatalf("content pauses between batches: maximum growth gap %s", maxGap)
	}
	if lastLength < 68 || lastLength > 80 {
		t.Fatalf("display fell behind or invented text: %d of 80", lastLength)
	}
	d.Items[0].Final = true
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(700 * time.Millisecond)
	if !ui.HasText(string(source[:80])) || ui.FramePending() {
		t.Fatal("completion leaves a delayed or animating tail")
	}
}

func TestLiveReplyRevealsBurstAndDrainsAfterCompletion(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Session.Status = "running"
	d.Items = nil
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: replyBurst}}
	m.Details["s"] = d
	ui.Frame()
	if ui.HasText("Stream tail.") {
		t.Fatal("a newly arrived reply appears in full instead of progressively")
	}
	ui.Advance(50 * time.Millisecond)
	if !ui.HasText("First") || ui.HasText("Stream tail.") {
		t.Fatal("reply has no intermediate readable prefix")
	}
	d.Items[0].Final = true
	m.Details["s"] = d
	ui.Frame()
	if ui.HasText("Stream tail.") {
		t.Fatal("completion skips the buffered tail")
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText("Stream tail.") || ui.FramePending() {
		t.Fatal("completed reply did not finish revealing and stop animating")
	}
}

func TestOpenedLiveHistoryShowsExistingTextThenAnimatesOnlyAdditions(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Session.Status = "running"
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: "Already visible history."}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	if !ui.HasText("Already visible history.") {
		t.Fatal("opening a running session replays its old text")
	}
	d.Items[0].Text += " " + replyBurst
	m.Details["s"] = d
	ui.Frame()
	if !ui.HasText("Already visible history.") || ui.HasText("Stream tail.") {
		t.Fatal("new text is not revealed separately from existing history")
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText("Stream tail.") {
		t.Fatal("new live suffix did not finish revealing")
	}
}

func TestFinalPayloadAdditionStillReveals(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: "Prefix."}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	d.Items[0].Text += " " + replyBurst
	d.Items[0].Final = true
	m.Details["s"] = d
	ui.Frame()
	if ui.HasText("Stream tail.") {
		t.Fatal("text added by the final payload bypasses progressive reveal")
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText("Stream tail.") {
		t.Fatal("final payload suffix never appeared")
	}
}

func TestReplyDoesNotReplayWhenNavigatingOrReconnecting(t *testing.T) {
	for _, action := range []string{"navigate", "reconnect"} {
		t.Run(action, func(t *testing.T) {
			m := layoutModel()
			m.ConnectionEpoch = "before"
			d := m.Details["s"]
			d.Items = nil
			m.Details["s"] = d
			ui := n.NewTester(New(m, nil, "").View, 1440, 900)
			d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: replyBurst}}
			m.Details["s"] = d
			ui.Frame()
			ui.Advance(2 * time.Second)
			if action == "navigate" {
				m.SessionID = ""
				ui.Frame()
				m.SessionID = "s"
			} else {
				m.ConnectionEpoch = "after"
				d.Items[0].Text += " Reconnected history."
				m.Details["s"] = d
			}
			ui.Frame()
			if !ui.HasText("Stream tail.") || action == "reconnect" && !ui.HasText("Reconnected history.") {
				t.Fatal("existing text replays after navigation or history recovery")
			}
		})
	}
}

func TestReducedMotionShowsLiveReplyImmediately(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	ui.SetPreferences(n.Preferences{ReduceMotion: true})
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: replyBurst}}
	m.Details["s"] = d
	ui.Frame()
	if !ui.HasText("Stream tail.") || ui.FramePending() {
		t.Fatal("reduced motion still delays or animates live text")
	}
}

func TestReplyRemountAfterVirtualizationDoesNotReplay(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 700)
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: replyBurst}}
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(2 * time.Second)
	for i := 0; i < 60; i++ {
		d.Items = append(d.Items, domain.Item{ID: strings.Repeat("x", i+1), Kind: "user", Text: "Later message"})
	}
	m.Details["s"] = d
	ui.Frame()
	if ui.HasText("Stream tail.") {
		t.Fatal("test did not unmount the old reply")
	}
	v.lists["s"].ScrollTo(0, n.Start)
	ui.Frame()
	if !ui.HasText("Stream tail.") {
		t.Fatal("virtualized history replays an already revealed reply")
	}
}

func TestNewReplyInkFadesInWithoutFadingExistingText(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = []domain.Item{{ID: "old", Kind: "assistant", Text: "Stable preceding text.", Final: true}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetDark(dark)
		old, _ := ui.Find("Stable preceding text.")
		before := copyFrame(ui)
		d.Items = append(d.Items, domain.Item{ID: "live", Kind: "assistant", Text: "F"})
		m.Details["s"] = d
		ui.Frame()
		r, ok := ui.Find("F")
		if !ok {
			t.Fatal("short new reply did not start revealing")
		}
		contrast := func(frame *image.RGBA, r n.Rect) int {
			bg := frame.RGBAAt(int(r.X), int(r.Y))
			total := 0
			for y := int(r.Y); y < int(r.Y+r.H); y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					p := frame.RGBAAt(x, y)
					total += int(math.Abs(float64(int(p.R)-int(bg.R))) + math.Abs(float64(int(p.G)-int(bg.G))) + math.Abs(float64(int(p.B)-int(bg.B))))
				}
			}
			return total
		}
		start := contrast(ui.Image(), r)
		ui.Advance(40 * time.Millisecond)
		// The reply now reveals its actual height as well as its ink. Sample
		// the current clip, then compare the same pixels after fading settles.
		middleRect, _ := ui.Find("F")
		middle := contrast(ui.Image(), middleRect)
		ui.Advance(200 * time.Millisecond)
		endRect, _ := ui.Find("F")
		end := contrast(ui.Image(), endRect)
		settledMiddle := contrast(ui.Image(), middleRect)
		if !(start < middle && middle < settledMiddle && settledMiddle <= end) {
			t.Fatalf("new reply ink jumps instead of fading (dark=%v): %d -> %d -> %d", dark, start, middle, end)
		}
		if contrast(before, old) != contrast(ui.Image(), old) {
			t.Fatal("new reply animation also fades preceding text")
		}
	}
}

func TestStreamingKeepsHistoryReadingPositionAndComposerFocus(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	for i := 0; i < 70; i++ {
		d.Items = append(d.Items, domain.Item{ID: fmt.Sprint(i), Kind: "assistant", Text: fmt.Sprintf("History paragraph %d.", i), Final: true})
	}
	m.Details["s"] = d
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1440, 700)
	v.lists["s"].ScrollTo(21, n.Start)
	ui.Frame()
	if err := ui.Click("消息输入"); err != nil {
		t.Fatal(err)
	}
	ui.Type("Unsent draft")
	before, ok := ui.Find("History paragraph 20.")
	if !ok {
		t.Fatal("test did not reach older history")
	}
	d.Items = append(d.Items, domain.Item{ID: "live", Kind: "assistant", Text: replyBurst})
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(500 * time.Millisecond)
	after, ok := ui.Find("History paragraph 20.")
	if !ok || math.Abs(float64(after.Y-before.Y)) > .5 || ui.HasText("Stream tail.") {
		t.Fatal("new stream pulled the reader away from history")
	}
	if !ui.Focused("消息输入") || m.Drafts["session:s"] != "Unsent draft" {
		t.Fatal("new stream stole composer focus or changed the draft")
	}
}

func TestSelectionSurvivesLiveSuffixReveal(t *testing.T) {
	m := layoutModel()
	d := m.Details["s"]
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: "Selected prefix."}}
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	r, _ := ui.Find("Selected prefix.")
	ui.Press(r.X+1, r.Y+r.H/2)
	ui.Move(r.X+r.W-1, r.Y+r.H/2)
	ui.Release(r.X+r.W-1, r.Y+r.H/2)
	ui.Key(n.Cmd, n.KeyC)
	selected := ui.Clipboard()
	if selected == "" {
		t.Fatal("test did not select the prefix")
	}
	d.Items[0].Text += " " + replyBurst
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(100 * time.Millisecond)
	ui.SetClipboard("")
	ui.Key(n.Cmd, n.KeyC)
	if got := ui.Clipboard(); got != selected {
		t.Fatalf("rebuilding streaming spans changed selection: %q -> %q", selected, got)
	}
}

func TestRecoveredSnapshotFinishesAnOldRevealImmediately(t *testing.T) {
	m := layoutModel()
	m.ConnectionEpoch = "first"
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	d.Items = []domain.Item{{ID: "live", Kind: "assistant", Text: replyBurst}}
	m.Details["s"] = d
	ui.Frame()
	ui.Advance(40 * time.Millisecond)
	m.ConnectionEpoch = "reconnected"
	ui.Frame()
	if !ui.HasText("Stream tail.") || ui.FramePending() {
		t.Fatal("recovered history keeps replaying the old live buffer")
	}
}

func TestRecordedConversationCompletesItsVisibleTail(t *testing.T) {
	f, err := os.Open("../../tests/fixtures/codex/live.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := layoutModel()
	d := m.Details["s"]
	d.Items = nil
	m.Details["s"] = d
	ui := n.NewTester(New(m, nil, "").View, 1440, 900)
	normalizer := codex.NewNormalizer()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var frame struct {
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatal(err)
		}
		var source struct {
			Params struct{ ThreadID string } `json:"params"`
		}
		if err := json.Unmarshal(frame.Message, &source); err != nil {
			t.Fatal(err)
		}
		normalizer.Bind(source.Params.ThreadID, "s", true)
		events, _, err := normalizer.Feed(frame.Message)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range events {
			event.Sequence = m.Sequence + 1
			if err := m.ApplyEvent(event); err != nil {
				t.Fatal(err)
			}
		}
		ui.Frame()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if !ui.FramePending() {
		t.Fatal("recorded burst has no visual reveal after runtime completion")
	}
	ui.Advance(2 * time.Second)
	if !ui.HasText("SHIOJI_RECORDING_READY") || !ui.HasText("SHIOJI_RECORDING_DONE") || ui.FramePending() {
		t.Fatal("recorded turns did not finish their visible tails and settle")
	}
}

func TestCodeAddedToAnOpenFenceFadesWithoutReplayingPreviousLines(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := layoutModel()
		d := m.Details["s"]
		d.Items = []domain.Item{{ID: "code", Kind: "assistant", Text: "```go\n// Stable\n"}}
		m.Details["s"] = d
		ui := n.NewTester(New(m, nil, "").View, 1440, 900)
		ui.SetDark(dark)
		ui.Advance(300 * time.Millisecond)
		d.Items[0].Text += "r"
		m.Details["s"] = d
		ui.Frame()
		// Wait for the first buffered code character, then measure its ink
		// independently of the content-release delay.
		r, ok := ui.Find("// Stable\nr")
		for frame := 0; !ok && frame < 20; frame++ {
			ui.Advance(time.Second / 60)
			r, ok = ui.Find("// Stable\nr")
		}
		if !ok {
			t.Fatalf("new code did not render inside the existing fence: %v", ui.Texts())
		}
		first := copyFrame(ui)
		ui.Advance(40 * time.Millisecond)
		middle := copyFrame(ui)
		ui.Advance(200 * time.Millisecond)
		settled := ui.Image()
		changes := func(a, b *image.RGBA, top, bottom int) int {
			n := 0
			for y := top; y < bottom; y++ {
				for x := int(r.X); x < int(r.X+r.W); x++ {
					if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
						n++
					}
				}
			}
			return n
		}
		line := int(r.Y + r.H/2)
		if changes(first, middle, line, int(r.Y+r.H)) == 0 || changes(middle, settled, line, int(r.Y+r.H)) == 0 {
			t.Fatalf("new code ink appears instantly instead of fading (dark=%v)", dark)
		}
		if changes(first, settled, int(r.Y), line) != 0 {
			t.Fatal("new code fades the already visible preceding line")
		}
		d.Items[0].Text += "eturn\n```"
		d.Items[0].Final = true
		m.Details["s"] = d
		ui.Frame()
		ui.Advance(500 * time.Millisecond)
		if err := ui.Click("Copy code"); err != nil {
			t.Fatal(err)
		}
		if got := ui.Clipboard(); got != "// Stable\nreturn\n" {
			t.Fatalf("code copy lost text or included markup: %q", got)
		}
	}
}
