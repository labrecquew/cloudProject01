package main

import (
	"testing"
	"time"
)

func TestCalendarDaysAcrossMonthBoundary(t *testing.T) {
	now := time.Date(2026, 10, 1, 15, 30, 0, 0, time.FixedZone("local", -4*3600))
	req, err := newSearchRequest("  space exploration  ", 2, 20, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if !req.From.Equal(want) || !req.To.Equal(now) || req.Query != "space exploration" {
		t.Fatalf("incorrect calendar range or query: %+v", req)
	}
}

func TestRejectInvalidInputs(t *testing.T) {
	for _, input := range []struct {
		query       string
		days, limit int
	}{
		{" ", 2, 20}, {"space", 0, 20}, {"space", -1, 20}, {"space", 2, 0}, {"space", 2, -1},
	} {
		if _, err := newSearchRequest(input.query, input.days, input.limit, time.Now()); err == nil {
			t.Errorf("accepted invalid input: %+v", input)
		}
	}
}
