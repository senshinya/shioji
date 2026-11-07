package app

// A remote read error suppresses retries for this visit, but must retain the
// next successful hydration's history-fold request across reconnects.
type historyLoadPhase uint8

const (
	historyUnloaded historyLoadPhase = iota
	historyOpening
	historyLoaded
	historyOpeningDeferred
)

func (s historyLoadPhase) loaded() bool  { return s == historyLoaded || s == historyOpeningDeferred }
func (s historyLoadPhase) opening() bool { return s == historyOpening || s == historyOpeningDeferred }
func (s historyLoadPhase) failed() historyLoadPhase {
	if s.opening() {
		return historyOpeningDeferred
	}
	return historyLoaded
}
func (s historyLoadPhase) invalidated() historyLoadPhase {
	if s.opening() {
		return historyOpening
	}
	return historyUnloaded
}
