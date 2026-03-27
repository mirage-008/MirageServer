package controller

import "testing"

func TestPollSessionOwnershipPrefersLatestSession(t *testing.T) {
	t.Parallel()

	h := &Mirage{}

	first := h.startPollSession(1)
	if !h.isCurrentPollSession(1, first) {
		t.Fatalf("expected first session to be current")
	}

	second := h.startPollSession(1)
	if first == second {
		t.Fatalf("expected unique session ids, got %d", first)
	}
	if h.isCurrentPollSession(1, first) {
		t.Fatalf("stale session should no longer be current after replacement")
	}
	if !h.isCurrentPollSession(1, second) {
		t.Fatalf("latest session should be current")
	}

	if h.finishPollSession(1, first) {
		t.Fatalf("stale session should not be able to clear current ownership")
	}
	if !h.isCurrentPollSession(1, second) {
		t.Fatalf("latest session should remain current after stale finish")
	}

	if !h.finishPollSession(1, second) {
		t.Fatalf("current session should be able to clear ownership")
	}
	if h.isCurrentPollSession(1, second) {
		t.Fatalf("session should no longer be current after finish")
	}
}

func TestPollSessionOwnershipIsPerMachine(t *testing.T) {
	t.Parallel()

	h := &Mirage{}

	a := h.startPollSession(1)
	b := h.startPollSession(2)

	if !h.isCurrentPollSession(1, a) || !h.isCurrentPollSession(2, b) {
		t.Fatalf("expected independent session ownership per machine")
	}

	if !h.finishPollSession(1, a) {
		t.Fatalf("expected machine 1 session to finish cleanly")
	}
	if !h.isCurrentPollSession(2, b) {
		t.Fatalf("finishing machine 1 session should not affect machine 2")
	}
}
