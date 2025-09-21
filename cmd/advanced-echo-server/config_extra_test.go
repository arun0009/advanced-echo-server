package main

import "testing"

func TestParseHelpers_Extra(t *testing.T) {
	if parseInt64("not-a-number") != 0 {
		t.Errorf("parseInt64 invalid should return 0")
	}
	if parseFloat64("not-a-number") != 0 {
		t.Errorf("parseFloat64 invalid should return 0")
	}
}
