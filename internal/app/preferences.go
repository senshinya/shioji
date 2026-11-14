package app

import (
	"encoding/json"
	"fmt"
	"slices"
)

// ClientPreferences are local presentation and input choices, never worker policy.
type ClientPreferences struct {
	Appearance          string                   `json:"appearance"`
	InterfaceFont       string                   `json:"interfaceFont"`
	ReplySize           int                      `json:"replySize"`
	CodeFont            string                   `json:"codeFont"`
	ReduceMotion        bool                     `json:"reduceMotion"`
	CollapseLatestTools bool                     `json:"collapseLatestTools,omitempty"`
	Shortcuts           ComposerShortcuts        `json:"shortcuts"`
	Notifications       *NotificationPreferences `json:"notifications,omitempty"`
}

type NotificationPreferences struct {
	Enabled    bool `json:"enabled"`
	Foreground bool `json:"foreground"`
	Completed  bool `json:"completed"`
	Attention  bool `json:"attention"`
	Failed     bool `json:"failed"`
	Sound      bool `json:"sound"`
}

// A newly added preference defaults on when an older file has no field, while
// a saved false remains an explicit user choice.
func (p *NotificationPreferences) UnmarshalJSON(data []byte) error {
	type fields NotificationPreferences
	value := fields{Foreground: true}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*p = NotificationPreferences(value)
	return nil
}

type ComposerShortcuts struct {
	Send    string `json:"send"`
	Newline string `json:"newline"`
	Steer   string `json:"steer"`
}

var ComposerBindings = []string{"Enter", "Shift+Enter", "Cmd+Enter", "Cmd+Shift+Enter", "Ctrl+Enter", "Alt+Enter"}

func DefaultShortcuts() ComposerShortcuts {
	return ComposerShortcuts{Send: "Enter", Newline: "Shift+Enter", Steer: "Cmd+Enter"}
}

func (p ClientPreferences) Normalized() ClientPreferences {
	if p.Notifications == nil {
		p.Notifications = &NotificationPreferences{Foreground: true, Completed: true, Attention: true, Failed: true, Sound: true}
	}
	if !slices.Contains([]string{"system", "light", "dark"}, p.Appearance) {
		p.Appearance = "system"
	}
	if p.InterfaceFont == "" {
		p.InterfaceFont = "system-ui"
	}
	if p.ReplySize < 12 || p.ReplySize > 20 {
		p.ReplySize = 14
	}
	if p.CodeFont == "" {
		p.CodeFont = "SF Mono"
	}
	s := p.Shortcuts
	if !slices.Contains(ComposerBindings, s.Send) || !slices.Contains(ComposerBindings, s.Newline) || !slices.Contains(ComposerBindings, s.Steer) || s.Send == s.Newline || s.Send == s.Steer || s.Newline == s.Steer {
		p.Shortcuts = DefaultShortcuts()
	}
	return p
}

// SetShortcut leaves all bindings intact when the new combination conflicts.
func (p *ClientPreferences) SetShortcut(action, binding string) error {
	if !slices.Contains(ComposerBindings, binding) {
		return fmt.Errorf("不支持的按键组合")
	}
	s := p.Shortcuts
	for _, entry := range []struct{ id, label, key string }{{"send", "发送或加入队列", s.Send}, {"newline", "换行", s.Newline}, {"steer", "立即介入当前轮", s.Steer}} {
		if entry.id != action && entry.key == binding {
			return fmt.Errorf("此按键已用于“%s”", entry.label)
		}
	}
	switch action {
	case "send":
		s.Send = binding
	case "newline":
		s.Newline = binding
	case "steer":
		s.Steer = binding
	default:
		return fmt.Errorf("未知的消息动作")
	}
	p.Shortcuts = s
	return nil
}
