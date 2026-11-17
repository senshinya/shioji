package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"shioji/internal/attachments"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"time"
)

// Transfers get their own authenticated stream so image traffic never queues
// ahead of Stop, answers, or the event subscription.
type AttachmentTarget struct {
	Directory, DeviceID, WorkspaceID string
	SSH                              *transport.SSH
	dial                             func(context.Context) (net.Conn, error)
}

func (m *Model) AttachmentTarget(directory, workspace string) (AttachmentTarget, error) {
	target := AttachmentTarget{Directory: directory, DeviceID: m.workspaceDevice(workspace), WorkspaceID: workspace}
	key := m.WorkspaceConnection(workspace)
	if key == "" {
		return target, errors.New("附件所属设备不可用")
	}
	if key != "local" {
		if state := m.DeviceStates[key]; state != nil {
			target.dial = state.dialStream
		}
		for _, c := range m.Connections {
			if c.ID == key {
				// A managed stream owns route selection, including any healthy
				// fallback. Do not expose a second saved route for UI preflight.
				if target.dial == nil {
					s := c.ActiveSSH()
					target.SSH = &s
				}
				return target, nil
			}
		}
		return target, errors.New("设备连接不存在")
	}
	return target, nil
}
func (t AttachmentTarget) openStream(ctx context.Context) (net.Conn, error) {
	if t.dial != nil {
		return t.dial(ctx)
	}
	if t.SSH != nil {
		return t.SSH.Dial(ctx)
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(t.Directory, "worker.sock"))
}
func (t AttachmentTarget) connect(ctx context.Context) (*protocol.Client, error) {
	conn, err := t.openStream(ctx)
	if err != nil {
		return nil, err
	}
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := protocol.Connect(handshake, conn, t.DeviceID)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if client.Minor < 6 {
		client.Close()
		return nil, errors.New("请先升级执行服务以使用附件")
	}
	return client, nil
}
func (t AttachmentTarget) Upload(ctx context.Context, staging *attachments.Repository, a domain.Attachment) error {
	return t.UploadProgress(ctx, staging, a, nil)
}

// Progress reports only bytes acknowledged by the owning worker.
func (t AttachmentTarget) UploadProgress(ctx context.Context, staging *attachments.Repository, a domain.Attachment, progress func(int64)) error {
	path, err := staging.Path(attachments.StagingWorkspace, a)
	if err != nil {
		return errors.New("本机附件副本不可用，请重新添加")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	client, err := t.connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	if a.Kind == domain.PathAttachmentResource && client.Minor < 47 {
		return errors.New("请先升级执行服务以上传路径引用文件")
	}
	for offset := int64(0); offset < a.Size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := make([]byte, min(int64(attachments.ChunkSize), a.Size-offset))
		if _, err = file.ReadAt(data, offset); err != nil {
			return err
		}
		var out attachments.Receipt
		if err = client.Call(ctx, "attachment.put", protocol.AttachmentPut{WorkspaceID: t.WorkspaceID, Attachment: a, Offset: offset, Data: data}, &out); err != nil {
			return err
		}
		if out.Attachment != a || out.Offset < offset+int64(len(data)) || out.Offset > a.Size || (out.Complete != (out.Offset == a.Size)) {
			return errors.New("附件上传回执不一致")
		}
		offset = out.Offset
		if progress != nil {
			progress(offset)
		}
	}
	return nil
}
func (t AttachmentTarget) Download(ctx context.Context, staging *attachments.Repository, a domain.Attachment) (string, error) {
	if path, err := staging.Path(attachments.StagingWorkspace, a); err == nil {
		return path, nil
	}
	client, err := t.connect(ctx)
	if err != nil {
		return "", err
	}
	defer client.Close()
	for offset := int64(0); offset < a.Size; {
		var out attachments.Receipt
		if err = client.Call(ctx, "attachment.get", protocol.AttachmentGet{WorkspaceID: t.WorkspaceID, ID: a.ID, Offset: offset}, &out); err != nil {
			return "", err
		}
		if out.Attachment != a || len(out.Data) == 0 || len(out.Data) > attachments.ChunkSize || out.Offset != offset+int64(len(out.Data)) || out.Offset > a.Size || out.Complete != (out.Offset == a.Size) {
			return "", errors.New("附件下载回执不一致")
		}
		if _, err = staging.Put(attachments.StagingWorkspace, a, offset, out.Data); err != nil {
			return "", err
		}
		offset = out.Offset
	}
	return staging.Path(attachments.StagingWorkspace, a)
}
func (c *Controller) AttachmentTask(work func() Update) {
	go func() {
		update := work()
		if update != nil {
			c.post(update)
		}
	}()
}

// Transfer updates and completion share the controller's ordered UI channel.
func (c *Controller) AttachmentTransfer(work func(report func(Update)) Update) {
	c.AttachmentTask(func() Update {
		return work(func(update Update) {
			// A hidden or busy UI can skip progress samples. Completion still
			// uses post, and carries the authoritative final receipt/error.
			select {
			case c.Updates <- update:
				c.mu.Lock()
				invalidate := c.invalidate
				c.mu.Unlock()
				if invalidate != nil {
					invalidate()
				}
			default:
			}
		})
	})
}
