package app

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"shioji/internal/domain"
)

// ClientPreferences are local presentation and input choices, never worker policy.
type ClientPreferences struct {
	Appearance              string                   `json:"appearance"`
	InterfaceFont           string                   `json:"interfaceFont"`
	InterfaceZoom           float32                  `json:"interfaceZoom"`
	ReplySize               int                      `json:"replySize"`
	CodeFont                string                   `json:"codeFont"`
	ReduceMotion            bool                     `json:"reduceMotion"`
	CollapseLatestTools     bool                     `json:"collapseLatestTools"`
	HistoryPageRounds       int                      `json:"historyPageRounds"`
	MobileReturnSends       bool                     `json:"mobileReturnSends,omitempty"`
	MobileComposerShortcuts *ComposerShortcuts       `json:"mobileComposerShortcuts,omitempty"`
	Shortcuts               ComposerShortcuts        `json:"shortcuts"`
	Notifications           *NotificationPreferences `json:"notifications,omitempty"`
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

func (p ClientPreferences) MobileShortcuts() ComposerShortcuts {
	if p.MobileComposerShortcuts != nil {
		return *p.MobileComposerShortcuts
	}
	if p.MobileReturnSends {
		return ComposerShortcuts{Send: "Enter", Newline: "Shift+Enter", Steer: "Cmd+Shift+Enter"}
	}
	return ComposerShortcuts{Send: "Cmd+Enter", Newline: "Enter", Steer: "Cmd+Shift+Enter"}
}

func (p ClientPreferences) MobileShortcutAction(binding string) string {
	bindings := p.MobileShortcuts()
	switch binding {
	case bindings.Send:
		return "send"
	case bindings.Newline:
		return "newline"
	case bindings.Steer:
		return "steer"
	}
	if p.MobileComposerShortcuts == nil {
		// Before assigning the first custom binding, retain the shortcuts
		// that the older Return setting made available together.
		switch binding {
		case "Cmd+Enter":
			return "send"
		case "Shift+Enter":
			return "newline"
		}
	}
	return ""
}

func (p *ClientPreferences) SetMobileShortcut(action, binding string) error {
	s := p.MobileShortcuts()
	if err := s.set(action, binding); err != nil {
		return err
	}
	if s != p.MobileShortcuts() {
		p.MobileComposerShortcuts = &s
	}
	return nil
}

func (p *ClientPreferences) ResetMobileShortcuts() {
	p.MobileComposerShortcuts = nil
	p.MobileReturnSends = false
}

func (s ComposerShortcuts) valid() bool {
	return slices.Contains(ComposerBindings, s.Send) && slices.Contains(ComposerBindings, s.Newline) &&
		slices.Contains(ComposerBindings, s.Steer) && s.Send != s.Newline && s.Send != s.Steer && s.Newline != s.Steer
}

func (p ClientPreferences) Normalized() ClientPreferences {
	if p.InterfaceZoom < .75 || p.InterfaceZoom > 1.5 || math.IsNaN(float64(p.InterfaceZoom)) {
		p.InterfaceZoom = 1
	}
	if p.HistoryPageRounds < 1 || p.HistoryPageRounds > domain.MaxHistoryRounds {
		p.HistoryPageRounds = domain.DefaultHistoryRounds
	}
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
	if !p.Shortcuts.valid() {
		p.Shortcuts = DefaultShortcuts()
	}
	if p.MobileComposerShortcuts != nil && !p.MobileComposerShortcuts.valid() {
		p.MobileComposerShortcuts = nil
	}
	return p
}

// SetShortcut leaves all bindings intact when the new combination conflicts.
func (p *ClientPreferences) SetShortcut(action, binding string) error {
	return p.Shortcuts.set(action, binding)
}

func (s *ComposerShortcuts) set(action, binding string) error {
	if !slices.Contains(ComposerBindings, binding) {
		return fmt.Errorf("不支持的按键组合")
	}
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
	return nil
}
