package ui

// Registered only by VirtualTextSelection. Core input/layout must not link the
// optional retention implementation into applications that never use it.
type virtualTextHooks struct {
	clear           func()
	keepMissing     func() bool
	restore         func() bool
	remember        func()
	copy            func() bool
	copyAllowed     func() bool
	releaseFocusRow func(*node) bool
}
