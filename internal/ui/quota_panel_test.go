package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	n "github.com/egoist/mygo/ui"
	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
)

func quotaTestModel() *app.Model {
	m := deviceUIModel()
	m.LocalDeviceName = "Work Mac"
	m.Connections = []app.DeviceConnection{
		{ID: "srv", DeviceID: "srv-id", Name: "Build Linux", SSH: transport.SSH{Target: "dev@srv", Worker: "/worker"}},
	}
	m.DeviceStates = map[string]*app.DeviceState{
		"local": {
			Connected:   true,
			WorkerMinor: protocol.QuotaMinor,
			Readiness: domain.Readiness{Harnesses: []domain.HarnessInfo{
				{ID: "codex", Name: "Codex", State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "chatgpt", State: "authenticated"}},
				{ID: "antigravity", Name: "Antigravity", State: domain.RuntimeReady},
				{ID: "agy-custom", Name: "AGY Custom", Profile: &domain.AgentProfile{ID: "agy-custom", HarnessID: "antigravity", Mode: domain.AgentProfileAccount}},
				{ID: "codex-api", Name: "Codex API", Profile: &domain.AgentProfile{ID: "codex-api", HarnessID: "codex", Mode: domain.AgentProfileAPI}},
			}},
		},
		"srv": {
			Connected:   true,
			WorkerMinor: protocol.QuotaMinor,
			Readiness: domain.Readiness{Harnesses: []domain.HarnessInfo{
				{ID: "codex", Name: "Codex", State: domain.RuntimeReady, Account: &domain.RuntimeAccount{Mode: "chatgpt", State: "authenticated"}},
				{ID: "deepseek", Name: "DeepSeek", State: domain.RuntimeReady, Configured: true},
			}},
		},
	}
	return m
}

func TestQuotaPanelEmptyPickerAndLocalOnlyNotice(t *testing.T) {
	m := quotaTestModel()
	v := New(m, nil, "")
	ui := n.NewTester(v.View, 1240, 820)
	ui.Advance(time.Second)

	if err := ui.Click("额度"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)

	// Heading must NOT carry a "仅本机" pill next to "额度".
	if ui.HasText("仅本机") {
		t.Fatal("quota heading must not show a 仅本机 badge")
	}
	// Empty state stays minimal: icon + single "选择 Profile" button, no verbose copy.
	if !ui.HasText("选择 Profile") || ui.HasText("尚未选择要监控额度的 Profile") {
		t.Fatalf("expected minimal empty state with button only: %v", ui.Texts())
	}

	// Open in-panel picker.
	if err := ui.Click("选择 Profile"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)

	if !ui.HasText("配置仅保存在本机，不同步到其他设备") {
		t.Fatalf("local-only notice missing inside picker: %v", ui.Texts())
	}
	// Unsupported profiles (AGY custom & Codex API) must NOT appear in picker.
	if ui.HasText("AGY Custom") || ui.HasText("Codex API") {
		t.Fatalf("unsupported profiles leaked into picker: %v", ui.Texts())
	}
	// Panel picker height stays capped <= 360.
	if panel, ok := ui.Find("全局快捷面板"); !ok || panel.H > 360 {
		t.Fatalf("picker panel height exceeded 360: %+v", panel)
	}

	// Selecting a profile persists into local-only QuotaPanel preferences.
	if err := ui.Click("选择 Codex（Work Mac）"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	if len(m.QuotaPanel.Targets) != 1 || m.QuotaPanel.Targets[0] != (app.QuotaTarget{ConnectionID: "local", ProfileID: "codex"}) {
		t.Fatalf("selection not saved locally: %+v", m.QuotaPanel)
	}
}

func TestQuotaPanelDynamicCodexDeepSeekTabsAndHeightCap(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			m := quotaTestModel()
			tLocalCodex := app.QuotaTarget{ConnectionID: "local", ProfileID: "codex"}
			tSrvCodex := app.QuotaTarget{ConnectionID: "srv", ProfileID: "codex"}
			tSrvDS := app.QuotaTarget{ConnectionID: "srv", ProfileID: "deepseek"}
			m.ToggleQuotaTarget(tLocalCodex)
			m.ToggleQuotaTarget(tSrvCodex)
			m.ToggleQuotaTarget(tSrvDS)

			now := time.Now().UTC()
			mins7d := int64(10080)
			rem68 := 68.0
			resetAt := now.Add(3 * time.Hour)
			// Codex with ONLY a 7d window (NO 5h window) -> must render 7d with zero filler copy.
			m.ApplyQuotaResult(tLocalCodex, domain.QuotaKindCodex, protocol.QuotaResult{
				Snapshot: &domain.QuotaSnapshot{
					HarnessID: "codex",
					Kind:      domain.QuotaKindCodex,
					PlanType:  "pro",
					FetchedAt: now,
					Windows:   []domain.QuotaWindow{{ID: "secondary", WindowMinutes: &mins7d, RemainingPercent: &rem68, ResetAt: &resetAt}},
				},
			}, nil, now)

			// Remote Codex with cached value + transient failure (dimmed stale + retry bar + alert dot).
			rem12 := 12.0
			m.ApplyQuotaResult(tSrvCodex, domain.QuotaKindCodex, protocol.QuotaResult{
				Snapshot: &domain.QuotaSnapshot{
					HarnessID: "codex",
					Kind:      domain.QuotaKindCodex,
					FetchedAt: now,
					Windows:   []domain.QuotaWindow{{ID: "secondary", WindowMinutes: &mins7d, RemainingPercent: &rem12}},
				},
			}, nil, now)
			m.ApplyQuotaResult(tSrvCodex, domain.QuotaKindCodex, protocol.QuotaResult{
				Error: "请求频率受限，请稍后重试",
			}, nil, now)

			// DeepSeek balance-only snapshot (no progress bar, no reset time).
			m.ApplyQuotaResult(tSrvDS, domain.QuotaKindDeepSeek, protocol.QuotaResult{
				Snapshot: &domain.QuotaSnapshot{
					HarnessID: "deepseek",
					Kind:      domain.QuotaKindDeepSeek,
					FetchedAt: now,
					Balance: &domain.QuotaBalance{
						Available: true,
						Entries: []domain.QuotaBalanceEntry{{
							Currency:        "CNY",
							TotalBalance:    "128.40",
							ToppedUpBalance: "100.00",
							GrantedBalance:  "28.40",
						}},
					},
				},
			}, nil, now)

			v := New(m, nil, "")
			ui := n.NewTester(v.View, 1240, 820)
			ui.SetDark(dark)
			ui.Advance(time.Second)

			// Collapsed alert dot must exist while quota drawer is closed.
			if m.OverallQuotaSeverity() == app.QuotaSeverityNone {
				t.Fatal("expected warning severity for low/errored profile")
			}

			if err := ui.Click("额度"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)

			// Normal quota panel height cap <= 310px.
			panel, ok := ui.Find("全局快捷面板")
			if !ok || panel.H > quickQuotaMaxHeight+0.5 {
				t.Fatalf("quota panel height cap violated: %+v", panel)
			}

			// Multi-profile tab bar shows "全部 3" + auto-disambiguated tabs.
			if !ui.HasText("全部 3") || !ui.HasText("Codex·Mac") || !ui.HasText("Codex·Linux") || !ui.HasText("DeepSeek") {
				t.Fatalf("expected multi-profile tab bar labels, got %v", ui.Texts())
			}

			// Switch to "全部 3" overview tab, then click compact row to jump to Codex·Mac detail.
			if err := ui.Click("全部 3"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if err := ui.Click("查看 Codex·Mac 额度详情"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("7d") || !ui.HasText("68%") {
				t.Fatalf("Codex 7d dynamic window missing: %v", ui.Texts())
			}
			if ui.HasText("5h") || ui.HasText("无 5h 窗口") || ui.HasText("无速率窗口") {
				t.Fatalf("fabricated 5h window or filler copy appeared: %v", ui.Texts())
			}

			// Tight spacing between tab bar and detail header (< 12 DIP gap).
			tabBar, ok1 := ui.Find("额度 Profile 切换栏")
			detailTitle, ok2 := ui.Find("Codex")
			if ok1 && ok2 {
				gap := detailTitle.Y - (tabBar.Y + tabBar.H)
				if gap > 12 {
					t.Fatalf("excessive gap between tab bar and detail content: %.1f DIP", gap)
				}
			}

			// Switch to remote Codex tab -> shows inline warning + retry + preserved cached 12%.
			if err := ui.Click("Codex·Linux"); err != nil {
				t.Fatal(err)
			}
			ui.Advance(time.Second)
			if !ui.HasText("请求频率受限，请稍后重试") || !ui.HasText("重试") || !ui.HasText("12%") {
				t.Fatalf("stale cache or retry warning bar missing: %v", ui.Texts())
			}

			// Switch to DeepSeek tab via ArrowRight keyboard shortcut (from focused tab) -> balance only, no reset time text.
			ui.Key(0, n.KeyRight)
			ui.Advance(time.Second)
			if !ui.HasText("¥128.40") || !ui.HasText("充值 ¥100.00 · 赠金 ¥28.40") {
				t.Fatalf("DeepSeek balance rendering missing: %v", ui.Texts())
			}
			if ui.HasText("后重置") {
				t.Fatalf("DeepSeek balance-only view must not display reset times: %v", ui.Texts())
			}
		})
	}
}

func TestCaptureQuotaPanelVerifyScreenshots(t *testing.T) {
	outDir := os.Getenv("SHIOJI_QUOTA_VERIFY_DIR")
	if outDir == "" {
		t.Skip("set SHIOJI_QUOTA_VERIFY_DIR to capture native acceptance screenshots")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	saveShot := func(name string, ui *n.Tester, cropW float32) {
		t.Helper()
		im := ui.Image()
		var out image.Image = im
		if cropW > 0 {
			maxX := int(cropW * 2)
			if maxX > im.Bounds().Max.X {
				maxX = im.Bounds().Max.X
			}
			out = im.SubImage(image.Rect(0, 0, maxX, im.Bounds().Max.Y))
		}
		f, err := os.Create(filepath.Join(outDir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, out); err != nil {
			t.Fatal(err)
		}
	}

	buildRichModel := func(now time.Time) *app.Model {
		m := quotaTestModel()
		tLocalCodex := app.QuotaTarget{ConnectionID: "local", ProfileID: "codex"}
		tLocalAGY := app.QuotaTarget{ConnectionID: "local", ProfileID: "antigravity"}
		tSrvCodex := app.QuotaTarget{ConnectionID: "srv", ProfileID: "codex"}
		tSrvDS := app.QuotaTarget{ConnectionID: "srv", ProfileID: "deepseek"}
		m.ToggleQuotaTarget(tLocalCodex)
		m.ToggleQuotaTarget(tLocalAGY)
		m.ToggleQuotaTarget(tSrvCodex)
		m.ToggleQuotaTarget(tSrvDS)
		m.SetQuotaActiveTab("all")

		mins7d := int64(10080)
		mins5h := int64(300)
		rem68 := 68.0
		rem84 := 84.0
		rem14 := 14.0
		rem72 := 72.0
		rem35 := 35.0
		reset7d := now.Add(4*24*time.Hour + 6*time.Hour)
		reset5h := now.Add(2*time.Hour + 18*time.Minute)
		resetAGY := now.Add(3*time.Hour + 40*time.Minute)

		// Local Codex: 7d only (no 5h window) + credits + real-time push indicator.
		m.ReceiveQuotaPush("local", domain.QuotaSnapshot{
			HarnessID: "codex",
			Kind:      domain.QuotaKindCodex,
			Source:    domain.QuotaSourcePush,
			PlanType:  "pro",
			Version:   3,
			FetchedAt: now.Add(-12 * time.Second),
			Windows:   []domain.QuotaWindow{{ID: "secondary", WindowMinutes: &mins7d, RemainingPercent: &rem68, ResetAt: &reset7d}},
			Credits:   &domain.QuotaCredits{HasCredits: true, Balance: "$18.50"},
		}, now.Add(-12*time.Second))

		// Local Antigravity: dynamic model groups.
		m.ApplyQuotaResult(tLocalAGY, domain.QuotaKindAntigravity, protocol.QuotaResult{
			Snapshot: &domain.QuotaSnapshot{
				HarnessID: "antigravity",
				Kind:      domain.QuotaKindAntigravity,
				Source:    domain.QuotaSourcePoll,
				FetchedAt: now.Add(-45 * time.Second),
				Groups: []domain.QuotaWindowGroup{
					{
						ID:   "gemini",
						Name: "Gemini 3 Pro",
						Windows: []domain.QuotaWindow{
							{ID: "b1", Label: "5h", RemainingPercent: &rem72, ResetAt: &resetAGY},
						},
					},
					{
						ID:   "claude",
						Name: "Claude Sonnet",
						Windows: []domain.QuotaWindow{
							{ID: "b2", Label: "Weekly", RemainingPercent: &rem35},
						},
					},
				},
			},
		}, nil, now.Add(-45*time.Second))

		// Remote Codex: cached 5h + 7d with transient 429 error & backoff countdown.
		m.ApplyQuotaResult(tSrvCodex, domain.QuotaKindCodex, protocol.QuotaResult{
			Snapshot: &domain.QuotaSnapshot{
				HarnessID: "codex",
				Kind:      domain.QuotaKindCodex,
				PlanType:  "plus",
				FetchedAt: now.Add(-2 * time.Minute),
				Windows: []domain.QuotaWindow{
					{ID: "primary", WindowMinutes: &mins5h, RemainingPercent: &rem14, ResetAt: &reset5h},
					{ID: "secondary", WindowMinutes: &mins7d, RemainingPercent: &rem84, ResetAt: &reset7d},
				},
			},
		}, nil, now.Add(-2*time.Minute))
		m.ApplyQuotaResult(tSrvCodex, domain.QuotaKindCodex, protocol.QuotaResult{
			Error:          "请求频率受限，请稍后重试",
			RetryAfterSecs: 45,
		}, nil, now)

		// Remote DeepSeek: balance only (no progress bar, no reset time).
		m.ApplyQuotaResult(tSrvDS, domain.QuotaKindDeepSeek, protocol.QuotaResult{
			Snapshot: &domain.QuotaSnapshot{
				HarnessID: "deepseek",
				Kind:      domain.QuotaKindDeepSeek,
				FetchedAt: now.Add(-20 * time.Second),
				Balance: &domain.QuotaBalance{
					Available: true,
					Entries: []domain.QuotaBalanceEntry{{
						Currency:        "CNY",
						TotalBalance:    "128.40",
						ToppedUpBalance: "100.00",
						GrantedBalance:  "28.40",
					}},
				},
			},
		}, nil, now.Add(-20*time.Second))

		return m
	}

	const winW, winH = 1040, 680
	const sidebarCropW = 244

	// 1. Light full window + sidebar states.
	now := time.Now().UTC()
	m := buildRichModel(now)
	v := New(m, nil, "")
	ui := n.NewTester(v.View, winW, winH)
	ui.SetScale(2)
	ui.SetDark(false)
	ui.Advance(time.Second)

	// Collapsed dock with alert dot.
	saveShot("verify-light-collapsed.png", ui, sidebarCropW)

	// Open quota panel -> "全部 4" full window hero & sidebar crop.
	if err := ui.Click("额度"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveShot("verify-light-hero.png", ui, 0)
	saveShot("verify-light-all.png", ui, sidebarCropW)

	// Codex·Mac (7d only, no 5h filler, tight tab spacing).
	if err := ui.Click("Codex·Mac"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveShot("verify-light-codex-7d.png", ui, sidebarCropW)

	// Antigravity dynamic model groups.
	if err := ui.Click("Antigravity"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveShot("verify-light-antigravity.png", ui, sidebarCropW)

	// Remote Codex stale cache + backoff error + retry bar.
	if err := ui.Click("Codex·Linux"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveShot("verify-light-error-backoff.png", ui, sidebarCropW)

	// DeepSeek balance-only (no progress bar, no reset time).
	// Tab 5 overflows the horizontal strip; ArrowRight selects it and triggers ScrollIntoView.
	ui.Key(0, n.KeyRight)
	ui.Advance(time.Second)
	if !ui.HasText("¥128.40") {
		t.Fatalf("DeepSeek tab did not activate via ArrowRight: %v", ui.Texts())
	}
	saveShot("verify-light-deepseek.png", ui, sidebarCropW)

	// In-panel cross-device Profile Picker (with local-only notice).
	if err := ui.Click("管理展示的 Profile"); err != nil {
		t.Fatal(err)
	}
	ui.Advance(time.Second)
	saveShot("verify-light-picker.png", ui, sidebarCropW)

	// Empty state.
	mEmpty := quotaTestModel()
	vEmpty := New(mEmpty, nil, "")
	uiEmpty := n.NewTester(vEmpty.View, winW, winH)
	uiEmpty.SetScale(2)
	uiEmpty.SetDark(false)
	uiEmpty.Advance(time.Second)
	if err := uiEmpty.Click("额度"); err != nil {
		t.Fatal(err)
	}
	uiEmpty.Advance(time.Second)
	saveShot("verify-light-empty.png", uiEmpty, sidebarCropW)

	// Dark mode: "全部 4" + Codex·Linux error/backoff.
	mDark := buildRichModel(now)
	vDark := New(mDark, nil, "")
	uiDark := n.NewTester(vDark.View, winW, winH)
	uiDark.SetScale(2)
	uiDark.SetDark(true)
	uiDark.Advance(time.Second)
	if err := uiDark.Click("额度"); err != nil {
		t.Fatal(err)
	}
	uiDark.Advance(time.Second)
	saveShot("verify-dark-all.png", uiDark, sidebarCropW)
	if err := uiDark.Click("Codex·Linux"); err != nil {
		t.Fatal(err)
	}
	uiDark.Advance(time.Second)
	saveShot("verify-dark-error-backoff.png", uiDark, sidebarCropW)
}
