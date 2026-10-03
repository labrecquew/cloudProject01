package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Article struct {
	Source struct {
		Name string `json:"name"`
	} `json:"source"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
}

type NewsClient struct {
	HTTP     *http.Client
	Endpoint string
	APIKey   string
}

type newsResponse struct {
	Status       string    `json:"status"`
	Code         string    `json:"code"`
	Message      string    `json:"message"`
	TotalResults int       `json:"totalResults"`
	Articles     []Article `json:"articles"`
}

func (c NewsClient) Search(ctx context.Context, req SearchRequest) ([]Article, int, error) {
	if c.APIKey == "" {
		return nil, 0, fmt.Errorf("set the NEWS_API_KEY environment variable for a search not covered by the database, or use -dry-run")
	}
	articles := make([]Article, 0)
	seen := make(map[string]bool)
	calls, received := 0, 0
	pageSize := min(req.Limit, 100)
	for page := 1; ; page++ {
		endpoint, err := url.Parse(c.Endpoint)
		if err != nil {
			return nil, calls, fmt.Errorf("invalid API endpoint")
		}
		params := endpoint.Query()
		params.Set("q", req.Query)
		params.Set("from", req.From.Format(time.RFC3339))
		params.Set("to", req.To.Format(time.RFC3339))
		params.Set("sortBy", "publishedAt")
		params.Set("pageSize", strconv.Itoa(pageSize))
		params.Set("page", strconv.Itoa(page))
		endpoint.RawQuery = params.Encode()
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, calls, fmt.Errorf("create API request: %w", err)
		}
		httpReq.Header.Set("X-Api-Key", c.APIKey)
		calls++
		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			return nil, calls, fmt.Errorf("news API request failed: %w", err)
		}
		var body newsResponse
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, calls, fmt.Errorf("news API HTTP %d (%s): %s", resp.StatusCode, body.Code, body.Message)
		}
		if decodeErr != nil {
			return nil, calls, fmt.Errorf("decode news response: %w", decodeErr)
		}
		if body.Status != "ok" {
			return nil, calls, fmt.Errorf("news API error (%s): %s", body.Code, body.Message)
		}
		received += len(body.Articles)
		for _, article := range body.Articles {
			if article.URL == "" || seen[article.URL] || article.PublishedAt.Before(req.From) || article.PublishedAt.After(req.To) {
				continue
			}
			seen[article.URL] = true
			articles = append(articles, article)
			if len(articles) == req.Limit {
				return articles, calls, nil
			}
		}
		if len(body.Articles) == 0 || received >= body.TotalResults {
			return articles, calls, nil
		}
	}
}
