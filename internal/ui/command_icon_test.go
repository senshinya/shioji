package ui

import (
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"testing"
	"time"
)

func TestCommandChevronStaysCompactBesideItsUnderscore(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, scale := range []float32{1, 2} {
			t.Run(fmt.Sprintf("dark=%v/scale=%g", dark, scale), func(t *testing.T) {
				ui := n.NewTester(func(c *n.Context) {
					n.Column(c).Fill().Padding(16).Background(c.Theme().Background).Children(func() {
						n.Row(c).Gap(8).AlignItems(n.Baseline).Children(func() {
							inlineSymbol(c, commandActivityIcon("sleep 1"), "等待", n.Font{Family: c.Theme().Font, Size: 12}, 14, c.Theme().TextMuted).Label("命令图标")
							n.Text(c, "等待").FontSize(12).FixedLineHeight(20)
						})
					})
				}, 180, 60)
				ui.SetDark(dark)
				ui.SetScale(scale)
				ui.Advance(time.Second)
				saveNativeSkillImage(t, ui, fmt.Sprintf("command-chevron-dark-%v-scale-%g.png", dark, scale))
				glyph, ok := ui.Find("命令图标")
				if !ok || glyph.W != 14 {
					t.Fatal("command icon slot changed", glyph)
				}
				im := ui.Image()
				bg := im.RGBAAt(0, 0)
				top, bottom := im.Bounds().Max.Y, -1
				// The left half contains the chevron, excluding the underscore.
				for y := 0; y < im.Bounds().Max.Y; y++ {
					for x := int(glyph.X * scale); x < int((glyph.X+7)*scale); x++ {
						p := im.RGBAAt(x, y)
						if max(absChannel(p.R, bg.R), absChannel(p.G, bg.G), absChannel(p.B, bg.B)) > 30 {
							top, bottom = min(top, y), max(bottom, y)
						}
					}
				}
				height := float32(bottom-top+1) / scale
				if height < 6 || height > 9 {
					t.Fatalf("chevron dominates the command icon: height=%g DIP", height)
				}
			})
		}
	}
}

func commandIconFixture(command string) (*View, *n.Tester) {
	v := toolTailFixture(2)
	d := v.Model.Details["s"]
	d.Items[1].Name = command
	d.Items[1].Text = ""
	d.Items[2].Name = "go test ./internal/ui"
	d.Items[2].Text = ""
	v.Model.Details["s"] = d
	ui := n.NewTester(v.View, 1240, 820)
	ui.SetDark(false)
	ui.Advance(time.Second)
	return v, ui
}
func commandGlyphPixels(ui *n.Tester, r n.Rect) []byte {
	im := ui.Image()
	pixels := make([]byte, 0, 16*20*4)
	for y := 0; y < 20; y++ {
		for x := 0; x < 16; x++ {
			p := im.RGBAAt(int(r.X)-24+x, int(r.Y)+y)
			pixels = append(pixels, p.R, p.G, p.B, p.A)
		}
	}
	return pixels
}
func TestGitCommandsUseDistinctNativeTimelineGlyph(t *testing.T) {
	for _, tc := range []struct {
		command string
		git     bool
	}{
		{"git status --short", true}, {"git -C '/repo with spaces' diff", true}, {"/usr/bin/git log -3", true},
		{"/bin/zsh -lc 'git diff --stat'", true}, {"bash -c \"git status\"", true},
		{"cd /repo && git diff", true}, {"git status; git diff", true}, {"GIT_PAGER=cat git log", true},
		{"env -u GIT_DIR git status", true}, {"command git status", true}, {"exec git diff", true},
		{"echo done | git hash-object --stdin", true}, {"printf 'git status'", false},
		{"rg git README.md", false}, {"echo \"git status; git diff\"", false},
		{"python3 -c 'print(\"git status\")'", false}, {"cat <<'EOF'\ngit status\nEOF", false},
		{"/bin/zsh -lc 'echo git'", false}, {"command -v git", false}, {"github status", false},
		{"git diff 'unterminated", false},
		{"env --help git", false}, {"env --version git", false},
		{"command -v git && git status", true}, {"command --help git", false}, {"exec --help git", false},
		{"env -i GIT_CONFIG_NOSYSTEM=1 /usr/bin/git status", true},
		{"exec -a launcher /usr/bin/git status", true},
	} {
		t.Run(tc.command, func(t *testing.T) {
			_, ui := commandIconFixture(tc.command)
			target, ok := ui.Find(tc.command)
			if !ok {
				t.Fatal("command missing")
			}
			normal, _ := ui.Find("go test ./internal/ui")
			a, b := commandGlyphPixels(ui, target), commandGlyphPixels(ui, normal)
			same := string(a) == string(b)
			if same == tc.git {
				t.Fatalf("Git glyph classification wrong: git=%v, glyph matches normal=%v", tc.git, same)
			}
		})
	}
}

// Retain evidence only when explicitly requested; the View is native MyGo.
func TestGitCommandIconNativeEvidence(t *testing.T) {
	if os.Getenv("SHIOJI_SKILL_SNAPSHOTS") == "" {
		t.Skip("native capture")
	}
	_, ui := commandIconFixture("/bin/zsh -lc 'git diff --stat'")
	saveNativeSkillImage(t, ui, "git-command-light.png")
}
