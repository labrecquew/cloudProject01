package main

import (
	"fmt"
	"strings"
	"time"
)

type SearchRequest struct {
	Query string
	Days  int
	Limit int
	From  time.Time
	To    time.Time
}

// Days are UTC calendar days: two days means today and yesterday.
func newSearchRequest(query string, days, limit int, now time.Time) (SearchRequest, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchRequest{}, fmt.Errorf("query must not be empty")
	}
	if days < 1 {
		return SearchRequest{}, fmt.Errorf("days must be a positive integer")
	}
	if limit < 1 {
		return SearchRequest{}, fmt.Errorf("limit must be a positive integer")
	}
	// Avoid overflowing calendar arithmetic for unreasonable input.
	if days > 36500 {
		return SearchRequest{}, fmt.Errorf("days cannot exceed 36500; your API plan may allow much less")
	}
	now = now.UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return SearchRequest{query, days, limit, midnight.AddDate(0, 0, -(days - 1)), now}, nil
}
