package ui

// Settings, device management and setup use the same native form rhythm.
// Compact setup tabs retain their own 12-DIP gap above form content.
const (
	deviceScenePortY              = 131 - 25 // Hardware connector minus SVG viewBox origin.
	deviceSceneMaxWidth           = 820
	deviceSceneDetailsWidth       = 480
	deviceSceneContentGap         = 12
	configurationDialogInset      = 16
	configurationFieldRadius      = 5
	configurationFieldInset       = 8
	configurationInset            = 32
	configurationSectionGap       = 24
	configurationHeadingGap       = 20
	configurationSubpageHeaderGap = 12
	configurationSectionHeader    = 36
	configurationTabHeight        = 36
	configurationRowHeight        = 40
	configurationControlHeight    = 28
	configurationTitleSize        = 20
	configurationTitleLine        = 28
	configurationLabelSize        = 13
	configurationValueSize        = 12
	configurationTextLine         = 20
	configurationSectionSize      = 14
	configurationListHeight       = 140
	profileChevronSize            = 16
	// disclosureRight uses a 24-point viewBox with its stroke ending at 16.75.
	deviceTabActionTrailingInset  = configurationHoverInset + (toolbarActionSize-profileChevronSize)/2 + (24-16.75)*profileChevronSize/24
	configurationHoverInset       = 8
	workspaceDirectoryChoiceInset = 4
	// Reserve room for centered add/remove button targets past their ink edge.
	workspaceDirectoryActionOutset = 8
	workspaceDirectoryScrollInset  = workspaceDirectoryActionOutset + configurationHoverInset
)

// Hooks share one stable scroll gutter across the toolbar, list and footer.
const (
	hookRowHeight      float32 = 64
	hookScrollGutter   float32 = 8
	hookCompactWidth   float32 = 448
	hookSearchMinWidth float32 = 128
)

const (
	scheduleEditorWidth      = 640
	scheduleEditorBackInset  = 11 // Left edge of the chevron ink within the 28 DIP button.
	scheduleEditorLabelWidth = 80
	scheduleEditorRowGap     = 12
	scheduleEditorDetailGap  = 8
)
