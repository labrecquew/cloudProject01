package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

var cacheTestNow = time.Date(2026, 10, 3, 18, 0, 0, 0, time.UTC)

type fakeNews struct {
	calls atomic.Int32
	fail  bool
	count int
}

func (f *fakeNews) Search(_ context.Context, req SearchRequest) ([]Article, int, error) {
	f.calls.Add(1)
	if f.fail {
		return nil, 1, fmt.Errorf("simulated API failure")
	}
	articles := make([]Article, 0)
	for i := 0; i < min(req.Limit, f.count); i++ {
		articles = append(articles, Article{URL: fmt.Sprintf("https://example.com/%s/%d", req.Query, i), Title: fmt.Sprintf("Article %d", i), PublishedAt: req.To.Add(-time.Minute)})
	}
	return articles, 1, nil
}

func testCache(t *testing.T) *Cache {
	t.Helper()
	cache, err := openCache(filepath.Join(t.TempDir(), "nested", "news.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	return cache
}

func TestCacheRequestSequence(t *testing.T) {
	cache := testCache(t)
	provider := &fakeNews{count: 20}
	service := SearchService{Cache: cache, Client: provider}
	for _, step := range []struct {
		name, query, source string
		days, limit, calls  int
		now                 time.Time
	}{
		{"first", "AI", "API", 2, 5, 1, cacheTestNow},
		{"repeat", "AI", "Database", 2, 5, 1, cacheTestNow.Add(time.Minute)},
		{"smaller limit", "AI", "Database", 2, 3, 1, cacheTestNow.Add(time.Minute)},
		{"larger limit", "AI", "API", 2, 8, 2, cacheTestNow.Add(time.Minute)},
		{"larger period", "AI", "API", 3, 8, 3, cacheTestNow.Add(time.Minute)},
		{"shorter covered period", "AI", "Database", 1, 3, 3, cacheTestNow.Add(time.Minute)},
		{"different topic", "space", "API", 2, 5, 4, cacheTestNow.Add(time.Minute)},
		{"new UTC day", "AI", "API", 2, 5, 5, cacheTestNow.AddDate(0, 0, 1)},
	} {
		t.Run(step.name, func(t *testing.T) {
			req, _ := newSearchRequest(step.query, step.days, step.limit, step.now)
			result, err := service.Search(context.Background(), req)
			if err != nil || result.Source != step.source || len(result.Articles) != step.limit || int(provider.calls.Load()) != step.calls {
				t.Fatalf("source=%s articles=%d total calls=%d error=%v", result.Source, len(result.Articles), provider.calls.Load(), err)
			}
			if result.Source == "Database" && result.APICalls != 0 {
				t.Fatal("cache hit reported an API call")
			}
		})
	}
}

func TestCachePersistsAcrossReopenWithoutAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "news.db")
	cache, err := openCache(path)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := newSearchRequest("AI", 2, 5, cacheTestNow)
	service := SearchService{Cache: cache, Client: &fakeNews{count: 5}}
	if _, err := service.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	cache, err = openCache(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	// An empty-key client would fail if the service tried to call it.
	service = SearchService{Cache: cache, Client: NewsClient{}}
	result, err := service.Search(context.Background(), req)
	if err != nil || result.Source != "Database" || len(result.Articles) != 5 {
		t.Fatalf("reopened cache failed: result=%+v error=%v", result, err)
	}
}

func TestShorterPeriodDoesNotAssumeCoverage(t *testing.T) {
	cache := testCache(t)
	broad, _ := newSearchRequest("AI", 7, 3, cacheTestNow)
	old := []Article{
		{URL: "https://example.com/old-a", PublishedAt: cacheTestNow.AddDate(0, 0, -4)},
		{URL: "https://example.com/old-b", PublishedAt: cacheTestNow.AddDate(0, 0, -5)},
		{URL: "https://example.com/old-c", PublishedAt: cacheTestNow.AddDate(0, 0, -6)},
	}
	if err := cache.Store(broad, old); err != nil {
		t.Fatal(err)
	}
	provider := &fakeNews{count: 3}
	service := SearchService{Cache: cache, Client: provider}
	narrow, _ := newSearchRequest("AI", 2, 3, cacheTestNow)
	result, err := service.Search(context.Background(), narrow)
	if err != nil || result.Source != "API" || len(result.Articles) != 3 {
		t.Fatalf("incorrect narrow-period coverage: result=%+v error=%v", result, err)
	}
}

func TestCacheRemembersRequestedLimitAndEmptyResults(t *testing.T) {
	for _, available := range []int{0, 2} {
		t.Run(fmt.Sprintf("available-%d", available), func(t *testing.T) {
			cache := testCache(t)
			provider := &fakeNews{count: available}
			service := SearchService{Cache: cache, Client: provider}
			req, _ := newSearchRequest("AI", 2, 5, cacheTestNow)
			for i := 0; i < 2; i++ {
				result, err := service.Search(context.Background(), req)
				if err != nil || len(result.Articles) != available {
					t.Fatalf("result=%+v error=%v", result, err)
				}
			}
			if provider.calls.Load() != 1 {
				t.Fatal("identical limited/empty search was fetched again")
			}
			req.Limit = 6
			result, err := service.Search(context.Background(), req)
			if err != nil || result.Source != "API" || provider.calls.Load() != 2 {
				t.Fatal("increased requested limit did not trigger API")
			}
		})
	}
}

func TestFailedExpansionPreservesCache(t *testing.T) {
	cache := testCache(t)
	provider := &fakeNews{count: 5}
	service := SearchService{Cache: cache, Client: provider}
	req, _ := newSearchRequest("AI", 2, 5, cacheTestNow)
	if _, err := service.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	provider.fail = true
	expanded := req
	expanded.Limit = 8
	if _, err := service.Search(context.Background(), expanded); err == nil {
		t.Fatal("expected expansion error")
	}
	if _, found, err := cache.Load(expanded); err != nil || found {
		t.Fatal("failed expansion was marked covered")
	}
	result, err := service.Search(context.Background(), req)
	if err != nil || result.Source != "Database" || len(result.Articles) != 5 {
		t.Fatal("previous successful cache was lost")
	}
}

func TestExpansionAddsArticlesWithoutDuplicates(t *testing.T) {
	cache := testCache(t)
	service := SearchService{Cache: cache, Client: &fakeNews{count: 10}}
	req, _ := newSearchRequest("AI", 2, 5, cacheTestNow)
	if _, err := service.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Limit = 8
	if _, err := service.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if err := cache.db.View(func(tx *bolt.Tx) error {
		if count := tx.Bucket(articlesBucket).Stats().KeyN; count != 8 {
			return fmt.Errorf("expected 8 stored articles, got %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentIdenticalSearchUsesOneFetch(t *testing.T) {
	cache := testCache(t)
	provider := &fakeNews{count: 5}
	service := SearchService{Cache: cache, Client: provider}
	req, _ := newSearchRequest("AI", 2, 5, cacheTestNow)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, err := service.Search(context.Background(), req)
			if err != nil || len(result.Articles) != 5 {
				t.Errorf("concurrent result failed: %+v %v", result, err)
			}
		}()
	}
	workers.Wait()
	if provider.calls.Load() != 1 {
		t.Fatalf("duplicate API fetches: %d", provider.calls.Load())
	}
}
