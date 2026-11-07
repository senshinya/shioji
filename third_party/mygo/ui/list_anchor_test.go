package ui

import "testing"

func TestListAnchorModes(t *testing.T) {
	var s ListState
	if s.anchored() {
		t.Fatal("new list has an anchor")
	}
	s.KeepRow(3, 40)
	if !s.anchored() || s.animatedAnchor() || s.sentAnchor() {
		t.Fatal("fixed reading anchor")
	}
	s.ScrollToRow(9, 80, Animation{})
	if !s.animatedAnchor() || s.sentAnchor() || !s.anchorPending() {
		t.Fatal("pending reading glide")
	}
	s.beginAnchor()
	if s.anchorPending() || !s.animatedAnchor() {
		t.Fatal("reading glide lost its mode")
	}
	s.AnchorRow(10, 100, Animation{})
	if !s.sentAnchor() || !s.anchorPending() {
		t.Fatal("pending sent anchor")
	}
	s.beginAnchor()
	if s.anchorPending() || !s.sentAnchor() {
		t.Fatal("sent anchor lost its mode")
	}
	s.ReleaseRow()
	if s.anchored() {
		t.Fatal("released anchor remains active")
	}
}
