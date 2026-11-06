package ui

// PointerPressedWithin observes a press on an element, its descendants or
// attached popovers without making the container clickable or changing focus.
func PointerPressedWithin(element Element) bool {
	e := element.node()
	if e == nil || !e.hasState() || len(e.c.rt.downs) == 0 || e.st.born == e.c.rt.frame {
		return false
	}
	rt := e.c.rt
	return rt.pressedWithin(rt.downs[len(rt.downs)-1], e)
}

// Inert keeps an element's layout and paint while excluding it and its
// descendants from pointer, keyboard and accessibility interaction. Unlike
// Disabled, it does not dim an outgoing view.
func Inert(element Element, inert bool) {
	e := element.node()
	if e == nil {
		return
	}
	if inert {
		e.flags |= flagInert
	} else {
		e.flags &^= flagInert
	}
}
