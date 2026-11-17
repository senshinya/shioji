package ui

import (
	"bytes"
	"context"
	"fmt"
	n "github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"strings"
	"testing"
	"time"
)

func remoteAttachmentFixture(t *testing.T) (*View, string) {
	t.Helper()
	m := layoutModel()
	m.WorkerMinor = 47
	m.Readiness.Capabilities.PathAttachments = true
	m.LocalDeviceID = "local-device"
	m.ActiveConnection = "air"
	m.Connections = []app.DeviceConnection{{ID: "air", DeviceID: "air-device", Name: "MacBook Air"}}
	m.Snapshot.Workspaces[0].DeviceID = "air-device"
	v := New(m, app.NewController(t.TempDir()), filepath.Join(t.TempDir(), "client.json"))
	t.Cleanup(func() { v.CloseTransient(); v.Saver.Close() })
	return v, m.DraftKey()
}

func stageRemoteAttachment(t *testing.T, v *View, key string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "界面参考.pdf")
	if err := os.WriteFile(file, []byte("%PDF-1.7\n\x00binary snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	v.addAttachmentFiles(key, []string{file})
	select {
	case update := <-v.Controller.Updates:
		update(v.Model)
	case <-time.After(3 * time.Second):
		t.Fatal("file preparation did not finish")
	}
}

func TestRemoteAttachmentWaitsForConsentAndCancelKeepsDraft(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	v.Model.Drafts[key] = "保留普通草稿"
	stageRemoteAttachment(t, v, key)
	if len(v.Model.Attachments[key]) != 1 || v.Model.Attachments[key][0].Ready {
		t.Fatal("remote file must be staged pending explicit consent", v.Model.Error)
	}
	a := v.Model.Attachments[key][0].Attachment
	path, err := v.staging().Path(attachments.StagingWorkspace, a)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Contains(data, []byte{0}) {
		t.Fatal("binary was rewritten as text")
	}
	ui := n.NewTester(v.View, 1280, 820)
	ui.Advance(time.Second)
	if !ui.HasText("上传文件到 MacBook Air？") || !ui.HasText("界面参考.pdf") {
		t.Fatal("missing native upload confirmation", ui.Texts())
	}
	if !v.preparingAttachments(key) {
		t.Fatal("unconfirmed file can be silently omitted from send")
	}
	if err := ui.Click("对此设备不再提示"); err != nil {
		t.Fatal(err)
	}
	if err := ui.Click("取消远程附件上传"); err != nil {
		t.Fatal(err)
	}
	if len(v.Model.Attachments[key]) != 0 || v.Model.Drafts[key] != "保留普通草稿" {
		t.Fatal("cancel changed the ordinary draft")
	}
	stageRemoteAttachment(t, v, key)
	ui.Frame()
	ui.Advance(time.Second)
	if !ui.HasText("上传文件到 MacBook Air？") {
		t.Fatal("cancel remembered an unconfirmed opt-out")
	}
}

func TestRemoteAttachmentTypingAndCompletedReferenceKeepNativeUndo(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	v.Model.Drafts[key] = "检查"
	stageRemoteAttachment(t, v, key)
	ui := n.NewTester(v.View, 1280, 820)
	ui.Click("消息输入")
	ui.Type("文件")
	if len(v.Model.Attachments[key]) != 1 || v.Model.Drafts[key] != "检查文件" {
		t.Fatal("typing lost pending staged file", v.Model.Attachments[key], v.Model.Drafts[key])
	}
	// Acknowledgement only changes transfer metadata; the editor must insert
	// its token without replacing the mounted native text document.
	v.Model.Attachments[key][0].Ready = true
	v.Model.Attachments[key][0].UploadApproved = true
	ui.Frame()
	files := v.Model.Attachments[key]
	if len(files) != 1 || files[0].InlinePosition == 0 || strings.TrimSpace(v.Model.Drafts[key]) != "检查文件" {
		t.Fatal("completed file did not enter native inline document", files, v.Model.Drafts[key])
	}
	ui.Key(n.Cmd, n.KeyZ)
	if len(v.Model.Attachments[key]) != 0 || v.Model.Drafts[key] != "检查文件" {
		t.Fatal("file insertion cannot be undone independently", v.Model.Drafts[key], v.Model.Attachments[key])
	}
	ui.Key(n.Cmd|n.Shift, n.KeyZ)
	if len(v.Model.Attachments[key]) != 1 || v.Model.Attachments[key][0].Attachment.Kind != domain.PathAttachmentResource {
		t.Fatal("redo lost remote file resource")
	}
}

func TestRemoteAttachmentConsentPersistsOnlyStableDeviceIdentity(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	v.confirmRemoteAttachments(key, true)
	if !v.Model.RemotePathUploads["air-device"] || v.Model.RemotePathUploads["air"] || !v.Model.Attachments[key][0].UploadApproved {
		t.Fatal("consent is not keyed to stable target identity")
	}
	state := filepath.Join(t.TempDir(), "state.json")
	if err := v.Model.Save(state); err != nil {
		t.Fatal(err)
	}
	next := app.NewModel()
	if err := next.Load(state); err != nil {
		t.Fatal(err)
	}
	if !next.RemotePathUploads["air-device"] || next.Attachments[key][0].Error == "" || next.Attachments[key][0].Ready {
		t.Fatal("restart silently resumes upload or forgets device consent")
	}
	v.Model.Connections = append(v.Model.Connections, app.DeviceConnection{ID: "another", DeviceID: "other-device", Name: "MacBook Air"})
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "other-workspace", DeviceID: "other-device"})
	v.Model.WorkspaceID, v.Model.SessionID, v.Model.ActiveConnection = "other-workspace", "", "another"
	stageRemoteAttachment(t, v, v.Model.DraftKey())
	if v.Model.Attachments[v.Model.DraftKey()][0].UploadApproved {
		t.Fatal("another device inherited consent through the same display name")
	}
}

func TestRemoteAttachmentNativeConfirmRemembersAfterExplicitAction(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	ui := n.NewTester(v.View, 1280, 820)
	ui.Advance(time.Second)
	if err := ui.Click("对此设备不再提示"); err != nil {
		t.Fatal(err)
	}
	if len(v.remoteUploadJobs) != 0 || v.Model.RemotePathUploads["air-device"] {
		t.Fatal("checkbox started transfer or persisted consent before confirmation")
	}
	if err := ui.Click("上传并引用"); err != nil {
		t.Fatal(err)
	}
	if !v.Model.RemotePathUploads["air-device"] || !v.Model.Attachments[key][0].UploadApproved {
		t.Fatal("explicit native confirmation did not persist opt-out")
	}
	v.cancelRemoteAttachments(key)
}

func TestRemoteAttachmentForeignWorkspaceCannotConfirmOrSend(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	v.Model.Drafts[key] = "保留"
	v.Model.WorkspaceID = "other"
	v.confirmRemoteAttachments(key, true)
	if v.Model.Attachments[key][0].UploadApproved || len(v.Model.RemotePathUploads) != 0 {
		t.Fatal("foreign workspace authorized upload")
	}
	if len(v.composerContent(key).files) != 0 || !v.preparingAttachments(key) {
		t.Fatal("foreign resource is visible as a ready reference or silently omitted")
	}
}

func TestRemoteAttachmentCheckboxDraftDoesNotFollowHomeToAnotherDevice(t *testing.T) {
	v, _ := remoteAttachmentFixture(t)
	v.Model.SessionID = ""
	key := v.Model.DraftKey()
	stageRemoteAttachment(t, v, key)
	ui := n.NewTester(v.View, 1280, 820)
	ui.Advance(time.Second)
	if err := ui.Click("对此设备不再提示"); err != nil {
		t.Fatal(err)
	}
	v.removeRemoteAttachment(key, v.Model.Attachments[key][0].Attachment.ID)
	v.Model.Connections = append(v.Model.Connections, app.DeviceConnection{ID: "other", DeviceID: "other-device", Name: "Other Mac"})
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "other", DeviceID: "other-device", Name: "Other"})
	v.Model.WorkspaceID, v.Model.ActiveConnection = "other", "other"
	stageRemoteAttachment(t, v, key)
	ui.Frame()
	ui.Advance(time.Second)
	if err := ui.Click("上传并引用"); err != nil {
		t.Fatal(err)
	}
	if v.Model.RemotePathUploads["other-device"] {
		t.Fatal("unconfirmed checkbox from another device silently enabled opt-out")
	}
	v.cancelRemoteAttachments(key)
}

func TestRemoteAttachmentLateJobCannotMutateRetryOrCancelledQueueEdit(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	v.confirmRemoteAttachments(key, false)
	a := v.Model.Attachments[key][0]
	jobKey := v.remoteUploadKey(key, a)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &remoteUploadJob{key: key, preparation: v.attachmentPreparationKey(key), id: a.Attachment.ID, device: a.DeviceID, workspace: a.WorkspaceID, cancel: cancel}
	v.remoteUploadJobs = map[string]*remoteUploadJob{jobKey: old}
	if v.remoteUploadDraft(v.Model, jobKey, old) != 0 {
		t.Fatal("current upload cannot find its draft")
	}
	retry := *old
	v.remoteUploadJobs[jobKey] = &retry
	if v.remoteUploadDraft(v.Model, jobKey, old) != -1 {
		t.Fatal("old job can mutate successor retry")
	}
	v.Model.QueueEdits["s"] = app.QueueEdit{ID: "queue", Token: "another-edit"}
	if v.remoteUploadDraft(v.Model, jobKey, &retry) != -1 {
		t.Fatal("late file callback can enter another queue edit generation")
	}
}

func TestRemoteAttachmentBinaryPreviewIsMetadataOnly(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	a := v.Model.Attachments[key][0].Attachment
	a.Name = "binary.md"
	v.openAttachment(a, "w", n.Rect{})
	if v.attachmentPreview.Loading || v.attachmentPreview.isMarkdown() || !strings.Contains(v.attachmentPreview.Text, "MacBook Air") {
		t.Fatal("binary preview is treated as content", v.attachmentPreview)
	}
}

func TestRemoteAttachmentNativePanelGeometry(t *testing.T) {
	for _, width := range []int{1280, 900} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				v, key := remoteAttachmentFixture(t)
				v.Model.Drafts[key] = "参考这份资料，调整侧栏的间距和层级。"
				stageRemoteAttachment(t, v, key)
				ui := n.NewTester(v.View, width, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				panel, ok := ui.Find("远程附件上传")
				composer, _ := ui.Find("消息编辑器")
				upload, button := ui.Find("上传并引用")
				if !ok || !button || panel.Y+panel.H > composer.Y+1 || panel.W > 768 || upload.X < panel.X || upload.X+upload.W > panel.X+panel.W+1 {
					t.Fatal("confirmation does not fit above native composer", panel, composer, upload)
				}
				if dir := os.Getenv("SHIOJI_REMOTE_ATTACHMENT_CAPTURE"); dir != "" {
					t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
					saveNativeSkillImage(t, ui, fmt.Sprintf("confirm-%d-dark-%v.png", width, dark))
				}
			})
		}
	}
}

func TestRemoteAttachmentFailuresFitMinimumWindowWithoutDuplicateStatus(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	v.Model.Drafts[key] = "保留\n多行\n普通\n输入"
	stageRemoteAttachment(t, v, key)
	base := v.Model.Attachments[key][0]
	v.Model.Attachments[key] = nil
	for i := range 8 {
		a := base
		a.Attachment.ID = domain.NewID()
		a.Attachment.Name = fmt.Sprintf("资料-%d.pdf", i)
		a.UploadApproved, a.Error = true, "连接已中断"
		v.Model.Attachments[key] = append(v.Model.Attachments[key], a)
	}
	ui := n.NewTester(v.View, 850, 600)
	ui.Advance(time.Second)
	if ui.HasText("附件暂时无法用于发送") {
		t.Fatal("path failure duplicates its own upload panel with generic status")
	}
	panel, ok := ui.Find("远程附件上传")
	retry, retryOK := ui.Find("重试上传")
	if !ok || !retryOK || panel.Y < 48 || retry.Y+retry.H > 600 || panel.H > 320 {
		t.Fatal("failed uploads consume minimum window", panel, retry)
	}
	list, ok := ui.Find("待上传文件列表")
	if !ok {
		t.Fatal("long upload list has no internal scrollport")
	}
	ui.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 300)
	ui.Advance(300 * time.Millisecond)
	if err := ui.Click("移除远程附件：资料-7.pdf"); err != nil {
		t.Fatal("last upload cannot be reached without moving fixed actions", err)
	}
	if len(v.Model.Attachments[key]) != 7 || v.Model.Drafts[key] != "保留\n多行\n普通\n输入" {
		t.Fatal("removing failed upload changed other content")
	}
}

// Production native renderer with explicit transfer-state fixtures. Real
// authenticated upload and app-server input are verified separately.
func TestRemoteAttachmentNativeStateEvidence(t *testing.T) {
	dir := os.Getenv("SHIOJI_REMOTE_ATTACHMENT_CAPTURE")
	if dir == "" {
		t.Skip("opt-in native captures")
	}
	t.Setenv("SHIOJI_SKILL_SNAPSHOTS", dir)
	for _, dark := range []bool{false, true} {
		for _, state := range []string{"uploading", "failed", "ready"} {
			t.Run(fmt.Sprintf("%s-dark-%v", state, dark), func(t *testing.T) {
				v, key := remoteAttachmentFixture(t)
				v.Model.Drafts[key] = "参考这份资料，调整侧栏的间距和层级。"
				resource, err := v.staging().SavePathResource("界面参考.pdf", bytes.Repeat([]byte{0, 0xff, 1, 2}, 600000))
				if err != nil {
					t.Fatal(err)
				}
				a := app.DraftAttachment{Attachment: resource, DeviceID: "air-device", WorkspaceID: "w", UploadApproved: true, Transferred: 1200000}
				if state == "failed" {
					a.Error = "与 MacBook Air 的连接已中断"
				}
				if state == "ready" {
					a.Ready = true
				}
				v.Model.Attachments[key] = []app.DraftAttachment{a}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				_ = ctx
				jobKey := v.remoteUploadKey(key, a)
				v.remoteUploadJobs = map[string]*remoteUploadJob{jobKey: {key: key, preparation: v.attachmentPreparationKey(key), id: a.Attachment.ID, workspace: a.WorkspaceID, device: a.DeviceID, cancel: cancel}}
				ui := n.NewTester(v.View, 1280, 820)
				ui.SetDark(dark)
				ui.Advance(time.Second)
				if state == "ready" {
					if v.preparingAttachments(key) || !ui.HasText("消息输入") {
						t.Fatal("ready reference still blocks composer")
					}
				} else if !ui.HasText("远程附件上传") {
					t.Fatal("missing native transfer state")
				}
				saveNativeSkillImage(t, ui, fmt.Sprintf("%s-1280-dark-%v.png", state, dark))
			})
		}
	}
}

func TestRemoteAttachmentNeverUsesOrdinaryUploader(t *testing.T) {
	v, key := remoteAttachmentFixture(t)
	stageRemoteAttachment(t, v, key)
	before := v.Model.Attachments[key][0]
	v.Model.Connections = append(v.Model.Connections, app.DeviceConnection{ID: "other", DeviceID: "other-device"})
	v.Model.Snapshot.Workspaces = append(v.Model.Snapshot.Workspaces, domain.Workspace{ID: "other", DeviceID: "other-device"})
	v.Model.WorkspaceID = "other"
	v.uploadDraft(key, 0)
	if got := v.Model.Attachments[key][0]; got != before || len(v.attachmentUploading) != 0 {
		t.Fatal("ordinary retry rebound the path resource to another device", got)
	}
}

func TestRemoteAttachmentClosingSnapshotCannotRestartRemovedOrReadyFile(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			v, key := remoteAttachmentFixture(t)
			stageRemoteAttachment(t, v, key)
			v.confirmRemoteAttachments(key, false)
			closingSnapshot := v.Model.Attachments[key][0]
			if completed {
				v.Model.Attachments[key][0].Ready = true
			} else {
				v.removeRemoteAttachment(key, closingSnapshot.Attachment.ID)
			}
			v.startRemoteUpload(key, closingSnapshot)
			if len(v.remoteUploadJobs) != 0 {
				t.Fatal("retained animation snapshot started an obsolete transfer")
			}
		})
	}
}
