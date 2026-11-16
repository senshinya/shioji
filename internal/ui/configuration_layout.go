package ui

// Settings, device management and setup use the same native form rhythm.
// Compact setup tabs retain their own 12-DIP gap above form content.
const (
	deviceScenePortY                = 131 - 25 // Hardware connector minus SVG viewBox origin.
	deviceSceneMaxWidth             = 820
	deviceSceneDetailsWidth         = 480
	configurationDialogInset        = 16
	configurationCompactDialogInset = 10
	configurationInset              = 32
	configurationSectionGap         = 24
	configurationHeadingGap         = 20
	configurationSectionHeader      = 36
	configurationTabHeight          = 36
	configurationRowHeight          = 40
	configurationControlHeight      = 28
	configurationTitleSize          = 20
	configurationTitleLine          = 28
	configurationLabelSize          = 13
	configurationValueSize          = 12
	configurationTextLine           = 20
	configurationSectionSize        = 14
	configurationListHeight         = 140
	configurationHoverInset         = 8
	workspaceDirectoryChoiceInset   = 4
)

// Hooks share one stable scroll gutter across the toolbar, list and footer.
const hookRowHeight float32 = 64
const hookScrollGutter float32 = 8
