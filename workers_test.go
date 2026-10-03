package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadBatchRejectsInvalidInput(t *testing.T) {
	valid := `{"id":"a","user":"alice","query":"AI","days":2,"limit":5}`
	for _, input := range []string{
		"", "not JSON", "null", valid + "\n" + valid,
		`{"id":"a","user":"alice","query":"AI","days":0,"limit":5}`,
		`{"id":"a","user":"alice","query":"AI","days":2,"limit":5,"typo":true}`,
		valid + " {}",
	} {
		if _, err := readBatch(strings.NewReader(input), cacheTestNow); err == nil {
			t.Errorf("accepted invalid batch: %q", input)
		}
	}
	if _, err := readBatch(strings.NewReader(valid+"\nnot JSON"), cacheTestNow); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("missing line number: %v", err)
	}
}

func TestBatchDryRunAndModeValidation(t *testing.T) {
	input := "\ufeff" + `{"id":"a","user":"alice","query":"AI","days":2,"limit":5}` + "\r\n\r\n"
	path := filepath.Join(t.TempDir(), "not-created.db")
	var out bytes.Buffer
	if err := runWithInput([]string{"-batch", "-", "-dry-run", "-db", path}, strings.NewReader(input), &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 valid requests") {
		t.Fatal(out.String())
	}
	// These errors are caught before database opening or API access.
	for _, args := range [][]string{
		{"-batch", "-", "-query", "AI"},
		{"-batch", "-", "-days", "2"},
		{"-batch", "-", "-workers", "0"},
	} {
		if err := runWithInput(args, strings.NewReader(input), &out, &out); err == nil {
			t.Fatalf("accepted conflicting/invalid flags: %v", args)
		}
	}
}

type blockingNews struct {
	started chan string
	release chan struct{}
}

func (b *blockingNews) Search(ctx context.Context, req SearchRequest) ([]Article, int, error) {
	b.started <- req.Query
	select {
	case <-b.release:
		return []Article{{URL: "https://example.com/" + req.Query, PublishedAt: req.To}}, 1, nil
	case <-ctx.Done():
		return nil, 1, ctx.Err()
	}
}

func TestBatchRunsDifferentTopicsConcurrently(t *testing.T) {
	cache := testCache(t)
	provider := &blockingNews{started: make(chan string, 4), release: make(chan struct{})}
	service := SearchService{Cache: cache, Client: provider}
	requests := make([]BatchRequest, 0)
	for i := 0; i < 4; i++ {
		query := fmt.Sprintf("topic-%d", i)
		req, _ := newSearchRequest(query, 2, 1, cacheTestNow)
		requests = append(requests, BatchRequest{query, "user", req})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var release sync.Once
	defer release.Do(func() { close(provider.release) })
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- processBatch(ctx, requests, 4, &service, &out) }()
	// All four API calls must start before any is allowed to finish. This
	// proves overlap without relying on a fragile elapsed-time threshold.
	for i := 0; i < 4; i++ {
		select {
		case <-provider.started:
		case <-ctx.Done():
			release.Do(func() { close(provider.release) })
			<-done
			t.Fatal("four independent topics did not start concurrently")
		}
	}
	release.Do(func() { close(provider.release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "successful=4 failed=0 API calls=4 workers=4") {
		t.Fatal(out.String())
	}
}

func TestBatchReturnsEveryRequestAndSharesCache(t *testing.T) {
	cache := testCache(t)
	provider := &fakeNews{count: 5}
	service := SearchService{Cache: cache, Client: provider}
	requests := make([]BatchRequest, 0)
	for i := 0; i < 120; i++ {
		req, _ := newSearchRequest(fmt.Sprintf("topic-%d", i%3), 2, 5, cacheTestNow)
		requests = append(requests, BatchRequest{fmt.Sprintf("id-%d", i), fmt.Sprintf("user-%d", i), req})
	}
	var out bytes.Buffer
	if err := processBatch(context.Background(), requests, 8, &service, &out); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 || strings.Count(out.String(), "Request: ") != 120 {
		t.Fatalf("calls=%d returned headers=%d", provider.calls.Load(), strings.Count(out.String(), "Request: "))
	}
	for _, request := range requests {
		if strings.Count(out.String(), fmt.Sprintf("Request: %s | User: %s\n", request.ID, request.User)) != 1 {
			t.Fatalf("missing/duplicate response for %s", request.ID)
		}
	}
	if !strings.Contains(out.String(), "successful=120 failed=0 API calls=3 workers=8") {
		t.Fatal("incorrect batch summary")
	}
	out.Reset()
	if err := processBatch(context.Background(), requests, 8, &service, &out); err != nil {
		t.Fatal(err)
	}
	if provider.calls.Load() != 3 || !strings.Contains(out.String(), "API calls=0 workers=8") {
		t.Fatal("repeat batch called API")
	}
}

type selectiveService struct{ calls atomic.Int32 }

func (s *selectiveService) Search(_ context.Context, req SearchRequest) (SearchResult, error) {
	s.calls.Add(1)
	if req.Query == "bad" {
		return SearchResult{APICalls: 1}, errors.New("simulated failure")
	}
	return SearchResult{Source: "Database", SnapshotTo: req.To}, nil
}

func TestBatchFailureDoesNotDiscardOtherResponses(t *testing.T) {
	good, _ := newSearchRequest("good", 2, 5, cacheTestNow)
	bad, _ := newSearchRequest("bad", 2, 5, cacheTestNow)
	requests := []BatchRequest{{"a", "alice", good}, {"b", "bob", bad}, {"c", "carol", good}}
	service := &selectiveService{}
	var out bytes.Buffer
	if err := processBatch(context.Background(), requests, 2, service, &out); err == nil {
		t.Fatal("expected batch error")
	}
	if service.calls.Load() != 3 || strings.Count(out.String(), "Request: ") != 3 || !strings.Contains(out.String(), "successful=2 failed=1 API calls=1") {
		t.Fatal(out.String())
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("simulated output failure") }

func TestBatchDrainsWorkersOnOutputError(t *testing.T) {
	req, _ := newSearchRequest("good", 2, 5, cacheTestNow)
	requests := make([]BatchRequest, 40)
	for i := range requests {
		requests[i] = BatchRequest{fmt.Sprint(i), "user", req}
	}
	service := &selectiveService{}
	if err := processBatch(context.Background(), requests, 4, service, brokenWriter{}); err == nil {
		t.Fatal("expected output error")
	}
	if service.calls.Load() != 40 {
		t.Fatal("workers were not drained")
	}
}
