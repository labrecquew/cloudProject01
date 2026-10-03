package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

type BatchRequest struct {
	ID     string
	User   string
	Search SearchRequest
}

// Validate the whole input before issuing requests so a typo cannot create a
// partially executed batch. One captured clock gives all jobs the same window.
func readBatch(in io.Reader, now time.Time) ([]BatchRequest, error) {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	requests := make([]BatchRequest, 0)
	ids := make(map[string]bool)
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if line == 1 {
			text = strings.TrimPrefix(text, "\ufeff")
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		var input struct {
			ID    string `json:"id"`
			User  string `json:"user"`
			Query string `json:"query"`
			Days  int    `json:"days"`
			Limit int    `json:"limit"`
		}
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			return nil, fmt.Errorf("batch line %d: %w", line, err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("batch line %d: expected exactly one JSON object", line)
		}
		input.ID, input.User = strings.TrimSpace(input.ID), strings.TrimSpace(input.User)
		if input.ID == "" || input.User == "" {
			return nil, fmt.Errorf("batch line %d: id and user must not be empty", line)
		}
		if ids[input.ID] {
			return nil, fmt.Errorf("batch line %d: duplicate request id %q", line, input.ID)
		}
		search, err := newSearchRequest(input.Query, input.Days, input.Limit, now)
		if err != nil {
			return nil, fmt.Errorf("batch line %d: %w", line, err)
		}
		ids[input.ID] = true
		requests = append(requests, BatchRequest{input.ID, input.User, search})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read batch near line %d: %w", line+1, err)
	}
	if len(requests) == 0 {
		return nil, fmt.Errorf("batch must contain at least one request")
	}
	return requests, nil
}

type requestSearcher interface {
	Search(context.Context, SearchRequest) (SearchResult, error)
}

type batchResult struct {
	Request BatchRequest
	Result  SearchResult
	Elapsed time.Duration
	Err     error
}

func processBatch(ctx context.Context, requests []BatchRequest, workers int, service requestSearcher, out io.Writer) error {
	if workers < 1 {
		return fmt.Errorf("workers must be a positive integer")
	}
	if len(requests) == 0 {
		return fmt.Errorf("batch must contain at least one request")
	}
	workers = min(workers, len(requests))
	started := time.Now()
	jobs := make(chan BatchRequest, workers)
	results := make(chan batchResult, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for request := range jobs {
				start := time.Now()
				result, err := service.Search(ctx, request.Search)
				results <- batchResult{request, result, time.Since(start), err}
			}
		}()
	}
	go func() {
		for _, request := range requests {
			jobs <- request
		}
		close(jobs)
		group.Wait()
		close(results)
	}()
	failed, calls := 0, 0
	var outputErr error
	// Only this goroutine writes output. Workers can finish in any order,
	// but each complete response remains grouped with its request identity.
	for result := range results {
		calls += result.Result.APICalls
		var block bytes.Buffer
		fmt.Fprintf(&block, "\nRequest: %s | User: %s\n", result.Request.ID, result.Request.User)
		writeRequest(&block, result.Request.Search)
		if result.Err != nil {
			failed++
			fmt.Fprintf(&block, "Request error: %s | API calls: %d\n", result.Err, result.Result.APICalls)
		} else {
			writeResult(&block, result.Result, result.Elapsed)
		}
		if _, err := out.Write(block.Bytes()); err != nil && outputErr == nil {
			outputErr = err
		}
	}
	_, err := fmt.Fprintf(out, "\nBatch complete: requests=%d successful=%d failed=%d API calls=%d workers=%d elapsed=%s\n",
		len(requests), len(requests)-failed, failed, calls, workers, time.Since(started).Round(time.Millisecond))
	if outputErr != nil {
		return outputErr
	}
	if err != nil {
		return err
	}
	if failed != 0 {
		return fmt.Errorf("%d batch request(s) failed; see individual request errors", failed)
	}
	return nil
}
