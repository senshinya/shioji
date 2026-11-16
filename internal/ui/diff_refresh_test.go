package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"testing"
	"time"
)

func TestChangesFailureStopsLoadingAndCanRecover(t *testing.T) {
	for _, minor := range []int{32, 33} {
		for _, dark := range []bool{false, true} {
			for _, cached := range []bool{false, true} {
				name := fmt.Sprintf("worker-%d-dark-%v-cached-%v", minor, dark, cached)
				t.Run(name, func(t *testing.T) {
					m := layoutModel()
					m.WorkerMinor, m.Connected, m.ConnectionEpoch = minor, true, "epoch"
					controller := app.NewController("")
					v := New(m, controller, "")
					s := v.changesFor(m.SessionID)
					if cached {
						s.load = changesLoaded
					}
					v.requestChanges(true)
					job := <-controller.Jobs
					ui := n.NewTester(func(c *n.Context) {
						var drop *app.InspectorLayout
						v.inspectorPane(c, c.Theme(), s, nil, "top", "changes", nil, false, 36, 420, &drop)
					}, 420, 300)
					ui.SetDark(dark)
					if !cached && !ui.HasText("读取更改…") {
						t.Fatal("pending initial request lost loading indicator", ui.Texts())
					}
					job.Apply(m, nil, errors.New("read failed"))
					ui.Frame()
					ui.Advance(time.Second)
					saveNativeSkillImage(t, ui, name+".png")
					if !ui.HasText("read failed") || ui.HasText("读取更改…") || ui.HasText("没有文件更改") || s.loading() {
						t.Fatal("failed request must show only its error", ui.Texts())
					}
					v.refreshChanges()
					retry := <-controller.Jobs
					var result any = []domain.ChangedFile{}
					if minor >= 33 {
						result = domain.ComparisonResult{Comparison: domain.DefaultDiffComparison()}
					}
					body, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					retry.Apply(m, body, nil)
					ui.Frame()
					ui.Advance(time.Second)
					if s.Error != "" || !s.loaded() || !ui.HasText("没有文件更改") || ui.HasText("读取更改…") {
						t.Fatal("successful retry did not recover empty state", ui.Texts())
					}
				})
			}
		}
	}
}

func TestNonRepositoryDiffShowsQuietStateAndRecovers(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{280, 420} {
			t.Run(fmt.Sprintf("dark-%v-width-%d", dark, width), func(t *testing.T) {
				recording := os.Getenv("SHIOJI_DIFF_RECORDING")
				if recording != "" {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", filepath.Join(recording, fmt.Sprintf("dark-%v-width-%d", dark, width)))
				}
				m := layoutModel()
				m.WorkerMinor, m.Connected, m.ConnectionEpoch = 33, true, "epoch"
				v := New(m, app.NewController(""), "")
				v.refreshChanges()
				job := <-v.Controller.Jobs
				ui := n.NewTester(func(c *n.Context) {
					v.inspector(c, c.Theme(), false, float32(width), float32(width))
				}, width, 300)
				ui.SetDark(dark)
				frame := 0
				record := func() {
					if recording == "" {
						return
					}
					for range 30 {
						ui.Advance(time.Second / 30)
						saveNativeSkillImage(t, ui, fmt.Sprintf("frame-%04d.png", frame))
						frame++
					}
				}
				record()
				failure := error(errors.New("not a Git repository"))
				if width == 420 {
					failure = &protocol.RemoteError{Code: "notGitRepository", Message: "repository review unavailable"}
				}
				job.Apply(m, nil, failure)
				ui.Frame()
				ui.Advance(time.Second)
				record()
				saveNativeSkillImage(t, ui, fmt.Sprintf("non-git-dark-%v-width-%d.png", dark, width))
				if !ui.HasText("当前目录不是 Git 仓库") || ui.HasText("读取更改…") || ui.HasText("not a Git repository") {
					t.Fatal("non-Git directory needs a quiet localized state", ui.Texts())
				}
				for _, label := range []string{"选择比较基准", "选择比较目标", "交换比较方向", "重试比较"} {
					if _, ok := ui.Find(label); ok {
						t.Fatal("non-Git state retained inapplicable control", label)
					}
				}
				if err := ui.Click("刷新更改"); err != nil {
					t.Fatal(err)
				}
				retry := <-v.Controller.Jobs
				record()
				body, _ := json.Marshal(domain.ComparisonResult{Comparison: domain.DefaultDiffComparison(), Files: []domain.ChangedFile{{Path: "new.txt", Kind: "add"}}})
				retry.Apply(m, body, nil)
				ui.Frame()
				ui.Advance(time.Second)
				record()
				if ui.HasText("当前目录不是 Git 仓库") || !ui.HasText("new.txt") {
					t.Fatal("Git initialization and refresh failed to restore review", ui.Texts())
				}
				if _, ok := ui.Find("选择比较基准"); !ok {
					t.Fatal("comparison controls did not return")
				}
			})
		}
	}
}

func TestDiffRefreshHasOneAtomicOwner(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor, m.Connected, m.ConnectionEpoch = 33, true, "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	s := v.changesFor(m.SessionID)
	s.Comparison = domain.DefaultDiffComparison()
	s.Path = "file.go"
	s.load = changesLoaded
	v.refreshChanges()
	job := <-controller.Jobs
	if job.Method != "git.compare" || len(controller.Jobs) != 0 {
		t.Fatal("refresh must own one atomic comparison", job.Method, len(controller.Jobs))
	}
}

func TestChangingFileDuringComparisonRejectsPreviousFile(t *testing.T) {
	m := layoutModel()
	m.WorkerMinor, m.Connected, m.ConnectionEpoch = 33, true, "epoch"
	controller := app.NewController("")
	v := New(m, controller, "")
	s := v.changesFor(m.SessionID)
	s.Path = "first.go"
	s.load = changesLoaded
	pair := domain.DefaultDiffComparison()
	v.requestComparison(s, pair)
	old := <-controller.Jobs
	v.openChangedFile("last.go")
	latest := <-controller.Jobs
	if latest.Method != "git.compare" {
		t.Fatalf("file change must join pending comparison, got %s", latest.Method)
	}
	apply := func(job app.Job, path string) {
		b, _ := json.Marshal(domain.ComparisonResult{Comparison: pair, Versions: &domain.FileVersions{Path: path, Comparison: pair, Old: "old", New: path}})
		job.Apply(m, b, nil)
	}
	apply(latest, "last.go")
	apply(old, "first.go")
	if s.Path != "last.go" || s.Versions.Path != "last.go" {
		t.Fatal("old comparison replaced selected file", s.Path, s.Versions.Path)
	}
}
