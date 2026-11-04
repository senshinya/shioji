package editor

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestExplicitDiffBackgroundsPreserveSemanticColors(t *testing.T) {
	theme := Theme{Background: ui.Hex("#ffffff"), Added: ui.Hex("#45634f"), Deleted: ui.Hex("#90564f"), AddedBackground: ui.Hex("#f0f5f1"), DeletedBackground: ui.Hex("#faf1ef")}
	colors := theme.diff(false)
	if colors.addedLine != theme.AddedBackground || colors.addedGutter != theme.AddedBackground || colors.deletedLine != theme.DeletedBackground || colors.deletedGutter != theme.DeletedBackground {
		t.Fatal("explicit diff fills were mixed")
	}
	if colors.added != theme.Added || colors.deleted != theme.Deleted {
		t.Fatal("fill override changed semantic foregrounds")
	}
}
