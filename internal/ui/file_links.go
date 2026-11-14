package ui

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	n "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"shioji/internal/app"
	"shioji/internal/attachments"
	"shioji/internal/domain"
)

type fileLinkState struct {
	session, workspace, epoch, path, error string
	loading, saving, openPending           bool
	cancel                                 context.CancelFunc
	bytes, total                           atomic.Int64
	result                                 *app.LinkedFile
	services                               n.Services
}

var fileLinkLocation = regexp.MustCompile(`:[0-9]+(?::[0-9]+)?$`)

func fileLinkPath(dest string) (string, error) {
	dest = fileLinkLocation.ReplaceAllString(dest, "")
	u, err := url.Parse(dest)
	if err != nil || u.Opaque != "" || u.User != nil || (u.Scheme != "" && u.Scheme != "file") || (u.Host != "" && u.Host != "localhost") || strings.HasPrefix(dest, "//") {
		return "", errors.New("不支持的文件链接")
	}
	path := u.Path
	if u.Scheme == "file" && !filepath.IsAbs(path) {
		return "", errors.New("文件地址必须是绝对路径")
	}
	if path == "" || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n\\") {
		return "", errors.New("文件路径无效")
	}
	return path, nil
}
func (v *View) fileLinkCurrent(s *fileLinkState) bool {
	if v.parent != nil && v.parent.Model.SecondarySessionID != s.session {
		return false
	}
	return v.fileLink == s && v.Model.SessionID == s.session && v.Model.ConnectionEpoch == s.epoch && v.Model.Details[s.session].Session.WorkspaceID == s.workspace
}
func (v *View) bindMarkdownLinks(c *n.Context, options *markdown.Options) {
	services := c.Services()
	source := v.Model.Details[v.Model.SessionID].Session
	options.OnLink = func(dest string) {
		if n.SafeToOpen(dest) {
			services.OpenURL(dest)
			return
		}
		if strings.HasPrefix(dest, "#") {
			options.Anchor = dest
			options.AnchorVersion++
			services.Invalidate()
			return
		}
		path, err := fileLinkPath(dest)
		if err != nil {
			v.Model.Error = err.Error()
			services.Invalidate()
			return
		}
		v.startFileLink(source, path, services)
	}
}
func (v *View) linkedFileDirectory() string {
	return filepath.Join(filepath.Dir(v.StatePath), "cache", "linked-files")
}
func (v *View) cancelFileLink() {
	if s := v.fileLink; s != nil {
		if s.cancel != nil {
			s.cancel()
		}
		v.fileLink = nil
	}
}
func (v *View) startFileLink(source domain.Session, path string, services n.Services) {
	v.cancelFileLink()
	s := &fileLinkState{session: source.ID, workspace: source.WorkspaceID, epoch: v.Model.ConnectionEpoch, path: path, loading: true, services: services}
	v.fileLink = s
	if v.Controller == nil || v.StatePath == "" || !v.Model.Connected {
		s.loading = false
		s.error = "设备未连接，无法读取文件"
		return
	}
	target, err := v.Model.AttachmentTarget(v.Controller.Directory, source.WorkspaceID)
	if err != nil {
		s.loading = false
		s.error = err.Error()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	s.cancel = cancel
	directory, controller := v.linkedFileDirectory(), v.Controller
	v.Controller.AttachmentTask(func() app.Update {
		defer cancel()
		var result app.LinkedFile
		if target.SSH != nil {
			var routed = *target.SSH
			routed, err = controller.TailnetSSH(routed)
			target.SSH = &routed
		}
		if err == nil {
			result, err = target.FetchFile(ctx, directory, source.ID, path, func(meta domain.FileResource, done int64) { s.total.Store(meta.Size); s.bytes.Store(done) })
		}
		var preview *attachmentPreview
		if err == nil && ctx.Err() == nil {
			preview, err = prepareLinkedPreview(result, source.WorkspaceID, source.ID, s.epoch)
		}
		if err == nil {
			err = ctx.Err()
		}
		return func(*app.Model) {
			if !v.fileLinkCurrent(s) {
				return
			}
			s.loading = false
			if err != nil {
				s.error = err.Error()
				return
			}
			s.result = &result
			if preview != nil {
				v.fileLink = nil
				v.attachmentPreview = preview
				return
			}
			if systemFilePreview(result.Path) {
				v.openLinkedFile(s)
			}
		}
	})
}
func prepareLinkedPreview(result app.LinkedFile, workspace, session, epoch string) (*attachmentPreview, error) {
	meta := result.Resource
	if systemFilePreview(result.Path) && !strings.HasPrefix(meta.MIME, "image/") {
		return nil, nil
	}
	if meta.Size <= 0 || meta.Size > 8<<20 {
		return nil, nil
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		return nil, err
	}
	a, err := attachments.Describe(meta.Name, data)
	if err != nil {
		return nil, nil
	}
	if a.SHA256 != result.SHA256 || a.Size != meta.Size {
		return nil, errors.New("文件副本已改变，请重新打开")
	}
	p := &attachmentPreview{Attachment: a, WorkspaceID: workspace, LocalPath: result.Path, SourceSessionID: session, SourceEpoch: epoch, presence: previewOpening}
	if strings.HasPrefix(a.MIME, "image/") {
		p.LoadedImage, err = decodeAttachmentImage(result.Path, 1600)
	} else {
		p.Text = string(data)
	}
	return p, err
}

func systemFilePreview(path string) bool {
	// Launch Services chooses the handler from the cached filename. Infer its
	// type locally: a peer's MIME must never authorize an executable extension.
	mime, _, err := mime.ParseMediaType(mime.TypeByExtension(strings.ToLower(filepath.Ext(path))))
	if err != nil {
		return false
	}
	return strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "audio/") || strings.HasPrefix(mime, "image/") || mime == "application/pdf" || mime == "application/rtf" || strings.HasPrefix(mime, "application/vnd.openxmlformats-officedocument.") || strings.HasPrefix(mime, "application/vnd.oasis.opendocument.")
}
func (v *View) openLinkedFile(s *fileLinkState) {
	if !v.fileLinkCurrent(s) || s.result == nil || !systemFilePreview(s.result.Path) {
		return
	}
	s.openPending = true
	s.services.Invalidate()
}
func (v *View) fileLinkDialog(c *n.Context, t *n.Theme) {
	s := v.fileLink
	if s == nil {
		return
	}
	if !v.fileLinkCurrent(s) {
		v.cancelFileLink()
		return
	}
	if s.openPending && s.result != nil {
		s.openPending = false
		address := (&url.URL{Scheme: "file", Path: s.result.Path}).String()
		c.OpenURLThen(address, func(err error) {
			if !v.fileLinkCurrent(s) {
				return
			}
			if err != nil {
				s.error = "无法打开文件：" + err.Error()
			} else {
				v.fileLink = nil
			}
		})
	}
	open := true
	n.DialogBase(c.Key("file-link-dialog"), &open, func(backdrop, panel n.Element) {
		backdrop.Background(n.RGBA(0, 0, 0, .2))
		width, _ := c.Size()
		panel.Width(min(float32(360), width-32)).Padding(configurationCompactDialogInset).Radius(10).Background(raised(t)).Border(1, t.Border).Gap(8).Label("文件链接")
		panel.Children(func() {
			n.Row(c).FillWidth().Gap(8).AlignItems(n.Center).Children(func() {
				title := filepath.Base(s.path)
				if s.result != nil {
					title = s.result.Resource.Name
				}
				n.Text(c, title).FontSize(14).MinWidth(0).Grow(1).SingleLine().Ellipsis("…").Tooltip(s.path)
				if iconButton(c, closeIcon, "关闭文件链接").Clicked() {
					v.cancelFileLink()
				}
			})
			if s.loading {
				total, done := s.total.Load(), s.bytes.Load()
				label := "正在连接所属设备…"
				if total > 0 {
					label = "已下载 " + storageCapacity(done) + " / " + storageCapacity(total)
				}
				n.Text(c, label).FontSize(12).TextColor(t.TextMuted)
				if total > 0 {
					n.Progress(c, float64(done)/float64(total)).FillWidth()
				}
				if configurationAction(c, "取消").Label("取消文件下载").Clicked() {
					v.cancelFileLink()
				}
				c.After(80 * time.Millisecond)
			} else {
				if s.error != "" {
					n.Text(c, s.error).FontSize(12).FixedLineHeight(20).TextColor(t.TextMuted)
				}
				if s.result != nil {
					n.Text(c, storageCapacity(s.result.Resource.Size)).FontSize(11).TextColor(t.TextMuted)
					n.Row(c).AlignSelf(n.Stretch).MarginX(-8).Justify(n.SpaceBetween).Gap(8).Children(func() {
						if systemFilePreview(s.result.Path) && configurationAction(c, "用默认应用打开").Clicked() {
							v.openLinkedFile(s)
						}
						if configurationAction(c, "打开所在文件夹").Clicked() {
							address := (&url.URL{Scheme: "file", Path: filepath.Dir(s.result.Path)}).String()
							c.OpenURLThen(address, func(err error) {
								if v.fileLinkCurrent(s) && err != nil {
									s.error = err.Error()
								}
							})
						}
						if configurationAction(c, "另存到本机").Disabled(s.saving).Clicked() {
							v.saveLinkedFile(s)
						}
					})
				} else if configurationAction(c, "重试").Clicked() {
					v.startFileLink(v.Model.Details[s.session].Session, s.path, c.Services())
				}
			}
		})
	})
	if !open {
		v.cancelFileLink()
	}
}
func (v *View) saveLinkedFile(s *fileLinkState) {
	if s.saving || s.result == nil || v.Controller == nil {
		return
	}
	s.saving = true
	source, name, parent := s.result.Path, s.result.Resource.Name, v.nativeWindow
	v.Controller.AttachmentTask(func() app.Update {
		destination, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "另存到本机", DefaultPath: name, CreateDirectories: true})
		if err == nil && destination != "" {
			err = copyLinkedFile(source, destination)
		}
		return func(*app.Model) {
			s.saving = false
			if v.fileLinkCurrent(s) && err != nil {
				s.error = err.Error()
			}
		}
	})
}
func copyLinkedFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(destination), ".shioji-save-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	defer out.Close()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), destination)
}
