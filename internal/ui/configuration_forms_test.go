package ui

import (
	n "github.com/egoist/mygo/ui"
	"testing"
)

func TestSharedConfigurationFieldPreservesNativeSelectionAndUndo(t *testing.T) {
	value := "check"
	ui := n.NewTester(func(c *n.Context) {
		n.Column(c).Width(320).Children(func() { configurationTextField(c, &value).Label("配置输入") })
	}, 400, 160)
	ui.Click("配置输入")
	ui.Key(n.Cmd, n.KeyA)
	ui.Type("design")
	if value != "design" {
		t.Fatal("native selection was not replaced", value)
	}
	ui.Key(n.Cmd, n.KeyZ)
	if value != "check" {
		t.Fatal("native undo history was lost", value)
	}
}
