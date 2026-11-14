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
}

func (m *Model) AttachmentTarget(directory, workspace string) (AttachmentTarget, error) {
	target := AttachmentTarget{Directory: directory, DeviceID: m.workspaceDevice(workspace), WorkspaceID: workspace}
	key := m.WorkspaceConnection(workspace)
	if key == "" {
		return target, errors.New("附件所属设备不可用")
	}
	if key != "local" {
		for _, c := range m.Connections {
			if c.ID == key {
				s := c.ActiveSSH()
				target.SSH = &s
				return target, nil
			}
		}
		return target, errors.New("设备连接不存在")
	}
	return target, nil
}
func (t AttachmentTarget) connect(ctx context.Context) (*protocol.Client, error) {
	var conn net.Conn
	var err error
	if t.SSH != nil {
		conn, err = t.SSH.Dial(ctx)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(t.Directory, "worker.sock"))
	}
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
	for offset := int64(0); offset < a.Size; {
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
