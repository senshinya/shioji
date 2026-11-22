package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shioji/internal/app"
	"shioji/internal/domain"
	"shioji/internal/mobileclient"
	"shioji/internal/protocol"
)

type mobileArchiveTarget struct {
	device           mobileclient.ArchiveDevice
	title, workspace string
	count            int
	input            protocol.ArchiveDeleteInput
}

func (t mobileArchiveTarget) method() string {
	if t.input.SessionID != "" {
		return "session.delete"
	}
	return "workspace.delete"
}

func archiveSessionTarget(row mobileclient.ArchivedConversation) mobileArchiveTarget {
	s := row.Session
	return mobileArchiveTarget{device: row.Device, title: s.Title, workspace: row.Workspace.ID,
		input: protocol.ArchiveDeleteInput{SessionID: s.ID, Revision: s.Revision, OperationID: domain.NewID()}}
}

func archiveWorkspaceTarget(row mobileclient.ArchivedWorkspace) mobileArchiveTarget {
	return mobileArchiveTarget{device: row.Device, title: row.Name, workspace: row.ID, count: row.Count,
		input: protocol.ArchiveDeleteInput{WorkspaceID: row.ID, Revision: row.Revision, OperationID: domain.NewID()}}
}

type mobileArchiveDeletion struct {
	targets, failed  []mobileArchiveTarget
	next             int
	running, batch   bool
	message, warning string
}

func (a *mobileApp) canUseArchiveDevice(device mobileclient.ArchiveDevice, minor int) bool {
	if a.preview {
		return device.ID == a.model.Snapshot.DeviceID
	}
	return device.ID != "" && device.Generation != "" && a.signer != nil &&
		a.savedDevice(device.ID).Endpoint.DeviceID == device.ID && (device.Minor == 0 || device.Minor >= minor)
}

func (a *mobileApp) archiveCommand(device mobileclient.ArchiveDevice, method string, input any, done func(json.RawMessage, error)) {
	saved, signer, route := a.savedDevice(device.ID), a.signer, a.routeDial()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		data, err := mobileclient.CallArchiveDevice(ctx, saved, device, signer, route, method, input)
		mygo.RunOnMain(func() { done(data, err) })
	}()
}

func (a *mobileApp) archiveOwner(device mobileclient.ArchiveDevice) *mobileApp {
	for _, owner := range []*mobileApp{a, a.parallelDevice} {
		if owner != nil && owner.model.Snapshot.DeviceID == device.ID && owner.model.Snapshot.Generation == device.Generation {
			return owner
		}
	}
	return nil
}

func (a *mobileApp) updateArchiveCatalog(device mobileclient.ArchiveDevice, update func(*domain.Snapshot)) {
	if owner := a.archiveOwner(device); owner != nil {
		update(&owner.model.Snapshot)
		return
	}
	catalog, ok := a.sessionCatalogs[device.ID]
	if !ok || catalog.Snapshot.DeviceID != device.ID || catalog.Snapshot.Generation != device.Generation {
		return
	}
	update(&catalog.Snapshot)
	a.sessionCatalogs[device.ID] = catalog
	a.saveBrowseCatalog(a.savedDevice(device.ID), catalog.Snapshot)
}

func (a *mobileApp) archiveChange(w *mygo.Window, req *mobileRequest, device mobileclient.ArchiveDevice,
	method string, input any, done func(json.RawMessage) error) {
	if req.busy || !a.pageCurrent(w) {
		return
	}
	req.busy, req.err = true, ""
	w.Invalidate()
	a.archiveCommand(device, method, input, func(data json.RawMessage, err error) {
		req.busy = false
		if err == nil {
			err = done(data)
		}
		if err != nil {
			req.err = err.Error()
		}
		a.refreshSessionCatalogs()
		a.refresh()
	})
}

func (a *mobileApp) restoreArchiveWorkspace(w *mygo.Window, req *mobileRequest, row mobileclient.ArchivedWorkspace) {
	source := row.Workspace
	if a.preview {
		a.changeWorkspaceArchive(w, req, source, false, nil)
		return
	}
	input := protocol.WorkspaceArchiveInput{WorkspaceID: source.ID, Revision: source.Revision}
	a.archiveChange(w, req, row.Device, "workspace.archive", input, decodePage(func(next domain.Workspace) error {
		if next.ID != source.ID || next.DeviceID != row.Device.ID || next.Revision <= source.Revision || next.Archived {
			return errors.New("恢复回执不属于所选工作区")
		}
		a.updateArchiveCatalog(row.Device, func(snapshot *domain.Snapshot) {
			snapshot.Workspaces = slices.Clone(snapshot.Workspaces)
			if i := slices.IndexFunc(snapshot.Workspaces, func(w domain.Workspace) bool { return w.ID == source.ID }); i >= 0 &&
				snapshot.Workspaces[i].Revision <= next.Revision {
				snapshot.Workspaces[i] = next
			}
		})
		return nil
	}))
}

func (a *mobileApp) restoreArchiveSession(w *mygo.Window, req *mobileRequest, row mobileclient.ArchivedConversation) {
	source := row.Session
	if a.preview {
		a.confirmSessionArchive(w, source)
		return
	}
	input := protocol.SessionChangeInput{SessionID: source.ID, Revision: source.Revision}
	a.archiveChange(w, req, row.Device, "session.archive", input, decodePage(func(next domain.Session) error {
		if next.ID != source.ID || next.WorkspaceID != row.Workspace.ID || next.ThreadID != source.ThreadID ||
			next.Context.DeviceID != "" && next.Context.DeviceID != row.Device.ID || next.Revision <= source.Revision || next.Archived {
			return errors.New("恢复回执不属于所选会话")
		}
		a.updateArchiveCatalog(row.Device, func(snapshot *domain.Snapshot) {
			snapshot.Sessions = slices.Clone(snapshot.Sessions)
			if i := slices.IndexFunc(snapshot.Sessions, func(s domain.Session) bool { return s.ID == source.ID }); i >= 0 &&
				snapshot.Sessions[i].Revision <= next.Revision {
				snapshot.Sessions[i] = next
			}
		})
		return nil
	}))
}

func (a *mobileApp) archiveRetention(c *ui.Context, w *mygo.Window, req *mobileRequest, busy bool, catalogs []mobileclient.DeviceCatalog) bool {
	showHeading := true
	daysName := func(days int) string {
		if days == 0 {
			return "永不"
		}
		return fmt.Sprintf("%d 天", days)
	}
	for _, catalog := range catalogs {
		policy := catalog.Snapshot.ArchivePolicy
		if policy == nil || policy.DeviceID == "" || policy.DeviceID != catalog.Snapshot.DeviceID {
			continue
		}
		if showHeading {
			mobileGroupHeading(c, "归档保留期")
			showHeading = false
		}
		device := catalog.ArchiveDevice()
		formChoice(c.Key(device.ID), device.Label(), daysName(policy.Days), func(menu *ui.Menu) {
			for _, days := range []int{30, 7, 14, 0} {
				menuChoice(menu, daysName(days), days == policy.Days, func() {
					if busy || req.busy || days == policy.Days || !a.canUseArchiveDevice(device, 40) {
						return
					}
					if a.preview {
						a.model.Snapshot.ArchivePolicy = &domain.ArchivePolicy{DeviceID: device.ID, Days: days, Revision: policy.Revision + 1}
						a.refresh()
						return
					}
					input := protocol.ArchivePolicyInput{Days: days, Revision: policy.Revision}
					a.archiveChange(w, req, device, "archive.configure", input, decodePage(func(next domain.ArchivePolicy) error {
						if next.DeviceID != device.ID || next.Days != days || next.Revision <= policy.Revision {
							return errors.New("归档设置回执与设备不一致")
						}
						a.updateArchiveCatalog(device, func(snapshot *domain.Snapshot) {
							if snapshot.ArchivePolicy == nil || snapshot.ArchivePolicy.Revision <= next.Revision {
								snapshot.ArchivePolicy = &next
							}
						})
						return nil
					}))
				})
			}
		})
	}
	return !showHeading
}

func (a *mobileApp) confirmArchiveDeletion(w *mygo.Window, targets []mobileArchiveTarget, all bool, scope string) {
	if !a.pageCurrent(w) || len(targets) == 0 || a.archiveConfirmation != nil || a.archiveDeletion != nil && a.archiveDeletion.running {
		return
	}
	first := targets[0]
	kind := "会话"
	if first.input.WorkspaceID != "" {
		kind = "工作区"
	}
	title, detail := "彻底删除"+kind+"？", "“"+first.title+"”将无法恢复。"
	if all {
		title = "彻底删除全部归档" + kind + "？"
		detail = fmt.Sprintf("全部 %d 个归档%s将无法恢复。操作包含搜索未显示的记录。", len(targets), kind)
		if scope != "" {
			detail = "“" + scope + "”中的" + detail
		} else if kind == "工作区" {
			detail = fmt.Sprintf("全部 %d 个归档工作区及其中的会话将无法恢复。操作包含搜索未显示的记录。", len(targets))
		}
	} else if kind == "工作区" {
		detail = fmt.Sprintf("“%s”及其中的 %d 个会话将无法恢复。", first.title, first.count)
	}
	var devices []mobileclient.ArchiveDevice
	counts := make(map[string]int)
	for _, target := range targets {
		if counts[target.device.ID] == 0 {
			devices = append(devices, target.device)
		}
		counts[target.device.ID]++
	}
	if len(devices) == 1 {
		detail += "\n\n设备 · " + devices[0].Label()
	} else {
		names := make([]string, 0, len(devices))
		for _, device := range devices {
			names = append(names, fmt.Sprintf("%s · %d 项", device.Label(), counts[device.ID]))
		}
		detail += "\n\n" + strings.Join(names, "\n")
	}
	detail += "\n\n删除将移除会话历史和附件，保留原项目文件和 Git 仓库。Shioji 创建的 worktree 在备份到分支后回收，忽略文件不进入备份。"
	a.archiveConfirmation = w
	ctx, cancel := context.WithCancel(context.Background())
	unregister := w.OnClosed(cancel)
	args := map[string]any{"window": w.NativeHandle(), "title": title, "detail": detail}
	go func() {
		defer cancel()
		var confirmed bool
		err := uikit.Call(ctx, "confirmArchiveDeletion", args, &confirmed)
		mygo.RunOnMain(func() {
			unregister()
			if a.archiveConfirmation == w {
				a.archiveConfirmation = nil
			}
			if err != nil || !confirmed || !a.pageCurrent(w) {
				return
			}
			d := &mobileArchiveDeletion{targets: targets, running: true, batch: all}
			a.archiveDeletion = d
			a.deleteNextArchive(d)
			a.refresh()
		})
	}()
}

func (a *mobileApp) archiveDeletionStatus(c *ui.Context, d *mobileArchiveDeletion) {
	if d == nil || d.running {
		return
	}
	if len(d.failed) != 0 {
		label(c, fmt.Sprintf("%d 项待核对 · %s", len(d.failed), d.message), captionTextSize, palette(c).warning)
		action(c.Key("retry"), "重试失败项", func() {
			d.targets, d.failed = d.failed, nil
			d.next, d.running, d.message = 0, true, ""
			a.deleteNextArchive(d)
			a.refresh()
		}).AlignSelf(ui.Start)
	}
	if d.warning != "" {
		label(c, d.warning, captionTextSize, palette(c).warning)
	}
	action(c.Key("dismiss"), "关闭提示", func() { a.archiveDeletion = nil; a.refresh() }).AlignSelf(ui.Start)
}

func (a *mobileApp) deleteNextArchive(d *mobileArchiveDeletion) {
	if d.next >= len(d.targets) {
		d.running = false
		if len(d.failed) == 0 && d.warning == "" {
			a.archiveDeletion = nil
		}
		a.refreshSessionCatalogs()
		a.refresh()
		return
	}
	target := d.targets[d.next]
	input := target.input
	finish := func(receipt domain.ArchiveDeletion, err error) {
		if err == nil {
			catalog := a.sessionCatalog(target.device.ID)
			err = verifyMobileArchiveReceipt(catalog.Snapshot, target, receipt)
		}
		if err != nil {
			d.failed = append(d.failed, target)
			if d.message == "" {
				d.message = target.device.Label() + " · " + target.title + " · " + err.Error()
			}
		} else {
			a.applyFleetArchiveDeletion(target.device, receipt)
			if receipt.Warning != "" {
				d.warning = receipt.Warning
			}
		}
		d.next++
		a.deleteNextArchive(d)
		a.refresh()
	}
	if !a.canUseArchiveDevice(target.device, 40) {
		finish(domain.ArchiveDeletion{}, errors.New("请连接并更新所属设备的执行服务"))
		return
	}
	if a.preview {
		receipt := domain.ArchiveDeletion{DeviceID: target.device.ID, WorkspaceID: input.WorkspaceID}
		if input.SessionID != "" {
			receipt.SessionIDs = []string{input.SessionID}
		} else {
			for _, row := range a.model.ArchivedSessions("", input.WorkspaceID) {
				receipt.SessionIDs = append(receipt.SessionIDs, row.Session.ID)
			}
		}
		finish(receipt, nil)
		return
	}
	a.archiveCommand(target.device, target.method(), input, func(raw json.RawMessage, err error) {
		var receipt domain.ArchiveDeletion
		if err == nil {
			err = json.Unmarshal(raw, &receipt)
		}
		finish(receipt, err)
	})
}

func (a *mobileApp) applyFleetArchiveDeletion(device mobileclient.ArchiveDevice, receipt domain.ArchiveDeletion) {
	if owner := a.archiveOwner(device); owner != nil {
		owner.model.ApplyArchiveDeletion(receipt)
		for _, id := range receipt.SessionIDs {
			delete(owner.history, id)
			owner.releaseMarkdownImages(id)
			if owner.parallel == id {
				owner.setParallel("")
			}
		}
		owner.save()
		return
	}
	a.updateArchiveCatalog(device, func(snapshot *domain.Snapshot) {
		m := app.NewModel()
		m.Snapshot = *snapshot
		m.Snapshot.Workspaces = slices.Clone(snapshot.Workspaces)
		m.Snapshot.Sessions = slices.Clone(snapshot.Sessions)
		m.Snapshot.PendingRequests = slices.Clone(snapshot.PendingRequests)
		m.Snapshot.Operations = slices.Clone(snapshot.Operations)
		m.ApplyArchiveDeletion(receipt)
		*snapshot = m.Snapshot
	})
}

func verifyMobileArchiveReceipt(snapshot domain.Snapshot, target mobileArchiveTarget, out domain.ArchiveDeletion) error {
	if out.DeviceID != target.device.ID || out.WorkspaceID != target.input.WorkspaceID {
		return errors.New("删除回执不属于所属设备或目标")
	}
	if id := target.input.SessionID; id != "" && (len(out.SessionIDs) != 1 || out.SessionIDs[0] != id) {
		return errors.New("删除回执不属于目标会话")
	}
	for _, id := range out.SessionIDs {
		if id == "" {
			return errors.New("设备返回了无效的会话标识")
		}
		if s := slices.IndexFunc(snapshot.Sessions, func(s domain.Session) bool { return s.ID == id }); s >= 0 &&
			snapshot.Sessions[s].WorkspaceID != target.workspace {
			return errors.New("删除回执包含其他工作区的会话")
		}
	}
	return nil
}
