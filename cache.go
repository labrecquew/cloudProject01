package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var articlesBucket = []byte("articles")
var searchesBucket = []byte("searches")

type Cache struct {
	db *bolt.DB
}

type searchRecord struct {
	Days           int       `json:"days"`
	RequestedLimit int       `json:"requestedLimit"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Exhausted      bool      `json:"exhausted"`
	URLs           []string  `json:"urls"`
}

type SearchResult struct {
	Articles   []Article
	Source     string
	APICalls   int
	SnapshotTo time.Time
}

func openCache(path string) (*Cache, error) {
	if path == "" {
		return nil, fmt.Errorf("database path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := bolt.Open(path, 0600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open database (only one process can use this file at a time): %w", err)
	}
	if err := db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{articlesBucket, searchesBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	return &Cache{db: db}, nil
}

func (c *Cache) Close() error { return c.db.Close() }

// JSON encoding keeps topics containing punctuation from colliding with other keys.
// The UTC date separates today's moving search window from yesterday's window.
func searchPrefix(req SearchRequest) []byte {
	key, _ := json.Marshal([]string{req.Query, req.To.UTC().Format("2006-01-02")})
	return append(key, 0)
}

func searchKey(req SearchRequest) []byte {
	return append(searchPrefix(req), []byte(strconv.Itoa(req.Days))...)
}

func (c *Cache) Load(req SearchRequest) (SearchResult, bool, error) {
	var result SearchResult
	found := false
	bestDays := 0
	err := c.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(searchesBucket).Cursor()
		prefix := searchPrefix(req)
		for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
			var record searchRecord
			if err := json.Unmarshal(value, &record); err != nil {
				return fmt.Errorf("decode saved search: %w", err)
			}
			if record.Days < req.Days || record.RequestedLimit < req.Limit || record.From.After(req.From) || record.To.After(req.To) {
				continue
			}
			articles := make([]Article, 0)
			for _, articleURL := range record.URLs {
				value := tx.Bucket(articlesBucket).Get([]byte(articleURL))
				if value == nil {
					return fmt.Errorf("saved search references a missing article")
				}
				var article Article
				if err := json.Unmarshal(value, &article); err != nil {
					return fmt.Errorf("decode saved article: %w", err)
				}
				if !article.PublishedAt.Before(req.From) && !article.PublishedAt.After(req.To) {
					articles = append(articles, article)
					if len(articles) == req.Limit {
						break
					}
				}
			}
			// A truncated broader window may not contain enough of the shorter
			// period. Reuse it only when sufficient results or exhaustion prove coverage.
			if record.Days != req.Days && len(articles) < req.Limit && !record.Exhausted {
				continue
			}
			if !found || record.Days < bestDays {
				result = SearchResult{Articles: articles, Source: "Database", SnapshotTo: record.To}
				found, bestDays = true, record.Days
			}
		}
		return nil
	})
	return result, found, err
}

func (c *Cache) Store(req SearchRequest, articles []Article) error {
	// Search returns fewer than the requested limit only after exhausting
	// the provider's results. Exactly reaching the limit does not prove exhaustion.
	record := searchRecord{Days: req.Days, RequestedLimit: req.Limit, From: req.From, To: req.To, Exhausted: len(articles) < req.Limit, URLs: make([]string, 0, len(articles))}
	return c.db.Update(func(tx *bolt.Tx) error {
		for _, article := range articles {
			value, err := json.Marshal(article)
			if err != nil {
				return err
			}
			if err := tx.Bucket(articlesBucket).Put([]byte(article.URL), value); err != nil {
				return err
			}
			record.URLs = append(record.URLs, article.URL)
		}
		value, err := json.Marshal(record)
		if err != nil {
			return err
		}
		return tx.Bucket(searchesBucket).Put(searchKey(req), value)
	})
}

type newsSearcher interface {
	Search(context.Context, SearchRequest) ([]Article, int, error)
}

// Topic/date locks prevent concurrent identical cache misses from issuing
// redundant API calls. Unrelated topics have separate locks.
type SearchService struct {
	Cache  *Cache
	Client newsSearcher
	mu     sync.Mutex
	locks  map[string]*sync.Mutex
}

func (s *SearchService) Search(ctx context.Context, req SearchRequest) (SearchResult, error) {
	key := string(searchPrefix(req))
	s.mu.Lock()
	if s.locks == nil {
		s.locks = make(map[string]*sync.Mutex)
	}
	lock := s.locks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.locks[key] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	result, found, err := s.Cache.Load(req)
	if err != nil {
		return SearchResult{}, fmt.Errorf("read cache: %w", err)
	}
	if found {
		return result, nil
	}
	articles, calls, err := s.Client.Search(ctx, req)
	if err != nil {
		return SearchResult{APICalls: calls}, err
	}
	if err := s.Cache.Store(req, articles); err != nil {
		return SearchResult{APICalls: calls}, fmt.Errorf("save cache: %w", err)
	}
	return SearchResult{Articles: articles, Source: "API", APICalls: calls, SnapshotTo: req.To}, nil
}
