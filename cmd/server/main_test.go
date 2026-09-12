package main

import "testing"

func TestRoomPattern(t *testing.T) {
	for _, id := range []string{"abc", "design-room_2", "ABC123"} {
		if !roomPattern.MatchString(id) {
			t.Fatalf("expected %q valid", id)
		}
	}
	for _, id := range []string{"ab", "room with spaces", "../../escape"} {
		if roomPattern.MatchString(id) {
			t.Fatalf("expected %q invalid", id)
		}
	}
}

func TestRandomID(t *testing.T) {
	a, b := randomID(6), randomID(6)
	if len(a) != 12 || a == b {
		t.Fatalf("unexpected IDs %q %q", a, b)
	}
}
