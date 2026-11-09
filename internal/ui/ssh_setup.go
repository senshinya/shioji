package ui

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/terminal"
	n "github.com/egoist/mygo/ui"
	"shioji/internal/transport"
)

type sshTerminalPhase uint8

const (
	sshTerminalLoading sshTerminalPhase = iota
	sshTerminalRunning
	sshTerminalFinished
	sshTerminalFailed
	sshTerminalClosed
)

// A UI-owned, transient PTY. Only this setup program is closed; worker and
// conversation processes are never owned by this view. No transcript is saved.
type sshSetupTerminal struct {
	mu    sync.Mutex
	term  *terminal.Terminal
	phase sshTerminalPhase
	code  int
	err   error
}

// Keep terminal colors independent of form controls, with neutral selections in
// both appearances. ANSI program colors retain the terminal's normal palette.
var sshTerminalLight, sshTerminalDark = func() (*terminal.Theme, *terminal.Theme) {
	light, dark := terminal.LightTheme(), terminal.DarkTheme()
	light.Background, light.Foreground = n.Hex("#f5f5f5"), n.Hex("#252525")
	light.Cursor, light.CursorText = light.Foreground, light.Background
	light.Selection, light.SelectionText = n.Hex("#d8d8d8"), light.Foreground
	dark.Background, dark.Foreground = n.Hex("#151515"), n.Hex("#dedede")
	dark.Cursor, dark.CursorText = dark.Foreground, dark.Background
	dark.Selection, dark.SelectionText = n.Hex("#414141"), n.Hex("#ffffff")
	return light, dark
}()

func newSSHSetupTerminal(command []string, font string, invalidate func()) *sshSetupTerminal {
	s := &sshSetupTerminal{}
	font = strings.TrimSpace(font)
	if font != "" {
		font += ", "
	}
	font += "monospace"
	go func() {
		t, err := terminal.New(terminal.Options{Command: command, Font: terminal.Font{Family: font, Size: 13, LineHeight: 1.15}, Padding: terminal.Padding{X: 14, Y: 12}, Theme: sshTerminalLight, DarkTheme: sshTerminalDark, Cursor: terminal.CursorBlock, Scrollback: 128 << 10, ImageLimit: -1, NoClipboardWrite: true, ClipboardRead: terminal.ClipboardDeny, OnExit: func(code int) {
			s.mu.Lock()
			if s.phase != sshTerminalClosed {
				s.phase = sshTerminalFinished
				s.code = code
			}
			s.mu.Unlock()
			invalidate()
		}})
		s.mu.Lock()
		if s.phase == sshTerminalClosed {
			s.mu.Unlock()
			if t != nil {
				t.Close()
			}
			return
		}
		s.term, s.err = t, err
		if err != nil {
			s.phase = sshTerminalFailed
		} else if s.phase != sshTerminalFinished {
			s.phase = sshTerminalRunning
		}
		s.mu.Unlock()
		invalidate()
	}()
	return s
}
func (s *sshSetupTerminal) snapshot() (*terminal.Terminal, sshTerminalPhase, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.term, s.phase, s.code, s.err
}
func (s *sshSetupTerminal) close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.phase == sshTerminalClosed {
		s.mu.Unlock()
		return
	}
	s.phase = sshTerminalClosed
	t := s.term
	s.term = nil
	s.mu.Unlock()
	if t != nil {
		t.Close()
	}
}
func (v *View) retireSSHSetupTerminal() {
	if s := v.sshTerminal; s != nil {
		v.sshTerminal = nil
		time.AfterFunc(260*time.Millisecond, s.close)
	}
}

// CloseTransient closes only UI-owned setup processes when the window exits.
func (v *View) CloseTransient() {
	if v.sshTerminal != nil {
		v.sshTerminal.close()
		v.sshTerminal = nil
	}
}
func (v *View) startSSHKeySetup() {
	port, _ := strconv.Atoi(strings.TrimSpace(v.sshPort))
	ssh := transport.SSH{Target: strings.TrimSpace(v.sshTarget), Port: port}
	command, err := ssh.KeySetupCommand()
	if err != nil {
		v.sshError = err.Error()
		return
	}
	if v.sshTerminal != nil {
		v.sshTerminal.close()
	}
	v.sshError = ""
	v.sshCheck = sshConfiguring
	v.sshTerminal = newSSHSetupTerminal(command, v.Model.Preferences.CodeFont, v.services.Invalidate)
}
func (v *View) syncSSHSetupTerminal() {
	if v.sshTerminal == nil || v.sshCheck != sshConfiguring {
		return
	}
	_, phase, code, err := v.sshTerminal.snapshot()
	switch phase {
	case sshTerminalFinished:
		if code == 0 {
			v.checkSSHDevice()
		} else {
			v.sshCheck = sshNeedsKey
			v.sshError = "SSH 密钥设置未完成"
		}
	case sshTerminalFailed:
		v.sshCheck = sshNeedsKey
		v.sshError = "无法启动终端：" + err.Error()
	}
}
func (v *View) sshSetupContent(c *n.Context, t *n.Theme) {
	review := n.Column(c.Key("host-key-review")).FillWidth().Clip()
	last := n.Local(review, "key", func() *transport.SSHHostKey { return v.sshHostKey })
	if v.sshHostKey != nil {
		*last = v.sshHostKey
	}
	animatedContentHeight(c, review, v.sshHostKey != nil, 240*time.Millisecond, func() float32 {
		if *last == nil {
			return 0
		}
		n.Column(c).FillWidth().Padding(12).Margin(8, 0, 0).Gap(8).Radius(6).Background(t.Surface).Children(func() {
			n.Row(c).FillWidth().AlignItems(n.Baseline).Children(func() {
				n.Text(c, "核对主机指纹").FontSize(13).FixedLineHeight(20).Grow(1)
				if deviceAction(c, "复制核对命令").Tooltip("在目标 Mac 上运行并比对指纹").Clicked() {
					c.WriteClipboard(`for file in /etc/ssh/ssh_host_*_key.pub; do ssh-keygen -lf "$file" -E sha256; done`)
				}
			})
			n.Text(c, (*last).Fingerprints).FillWidth().Font(v.Model.Preferences.CodeFont + ", monospace").FontSize(12).FixedLineHeight(20).Selectable()
		})
		return 0
	})
	panel := n.Column(c.Key("ssh-terminal-panel")).FillWidth().Clip()
	retained := n.Local(panel, "terminal", func() *sshSetupTerminal { return v.sshTerminal })
	if v.sshTerminal != nil {
		*retained = v.sshTerminal
	}
	animatedContentHeight(c, panel, v.sshTerminal != nil, 240*time.Millisecond, func() float32 {
		if *retained == nil {
			return 0
		}
		active, phase, _, _ := (*retained).snapshot()
		n.Column(c).FillWidth().Padding(12, 0, 0).Children(func() {
			caption := "ssh-copy-id"
			if phase == sshTerminalLoading {
				caption = "正在准备终端"
			}
			background, header, border := sshTerminalLight.Background, n.Hex("#ebebeb"), n.Hex("#d9d9d9")
			if t.Dark {
				background, header, border = sshTerminalDark.Background, n.Hex("#202020"), n.Hex("#363636")
			}
			_, height := c.Size()
			box := n.Column(c).FillWidth().Height(min(240, max(160, height-420))).Radius(6).Clip().Background(background).Border(1, border)
			box.Children(func() {
				n.Row(c.Key("terminal-caption")).FillWidth().Height(30).Padding(0, 14).AlignItems(n.Center).Background(header).Children(func() {
					inlineIconText(c, terminalIcon, caption, n.Font{Family: "monospace", Size: 11, Weight: 400}, 14, 8, t.TextMuted).ContentTransition(n.ContentTransition{Animation: animationPointer(c, 160*time.Millisecond)})
				})
				n.Box(c).FillWidth().Height(1).Background(border)
				if active != nil {
					terminal.View(c, active).FillWidth().Grow(1).MinHeight(0).AutoFocus().Label("SSH 设置终端").Transition(fadeMotion(c, 160*time.Millisecond))
				}
			})
		})
		return 0
	})
}
