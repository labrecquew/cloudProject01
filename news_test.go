package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchPaginationAndDuplicateFiltering(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	req, _ := newSearchRequest("space & science", 2, 3, now)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Api-Key") != "test-key" || r.URL.Query().Get("q") != req.Query || r.URL.Query().Get("from") != req.From.Format(time.RFC3339) {
			t.Error("incorrect API authentication, topic encoding, or date")
		}
		urls := []string{"https://example.com/a", "https://example.com/a", "https://example.com/b"}
		if r.URL.Query().Get("page") == "2" {
			urls = []string{"https://example.com/c"}
		}
		body := newsResponse{Status: "ok", TotalResults: 4}
		for _, u := range urls {
			body.Articles = append(body.Articles, Article{URL: u, Title: "Article", PublishedAt: now.Add(-time.Hour)})
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	client := NewsClient{server.Client(), server.URL, "test-key"}
	articles, calls, err := client.Search(context.Background(), req)
	if err != nil || len(articles) != 3 || calls != 2 || requests != 2 {
		t.Fatalf("articles=%d calls=%d requests=%d error=%v", len(articles), calls, requests, err)
	}
}

func TestSearchDoesNotTreatLaterPageFailureAsSuccess(t *testing.T) {
	now := time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)
	req, _ := newSearchRequest("space", 2, 2, now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"status":"error","code":"rateLimited","message":"quota exhausted"}`)
			return
		}
		json.NewEncoder(w).Encode(newsResponse{Status: "ok", TotalResults: 2, Articles: []Article{{URL: "https://example.com/a", PublishedAt: now}}})
	}))
	defer server.Close()
	articles, calls, err := (NewsClient{server.Client(), server.URL, "test-key"}).Search(context.Background(), req)
	if err == nil || articles != nil || calls != 2 {
		t.Fatalf("expected failed search; articles=%v calls=%d error=%v", articles, calls, err)
	}
}

func TestSearchRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "not JSON") }))
	defer server.Close()
	req, _ := newSearchRequest("space", 2, 20, time.Now())
	if _, _, err := (NewsClient{server.Client(), server.URL, "test-key"}).Search(context.Background(), req); err == nil {
		t.Fatal("accepted malformed API response")
	}
}
