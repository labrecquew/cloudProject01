package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func run(args []string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("cloudProject01", flag.ContinueOnError)
	flags.SetOutput(errOut)
	query := flags.String("query", "", "news topic (required)")
	days := flags.Int("days", 1, "UTC calendar days, including today")
	limit := flags.Int("limit", 20, "maximum number of articles")
	dbPath := flags.String("db", "data/news.db", "persistent database file")
	dryRun := flags.Bool("dry-run", false, "validate and display the search without calling the API")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments; put a multiword topic in quotes")
	}
	req, err := newSearchRequest(*query, *days, *limit, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Topic: %s\nDays: %d | Maximum articles: %d\nUTC range: %s to %s\n",
		req.Query, req.Days, req.Limit, req.From.Format(time.RFC3339), req.To.Format(time.RFC3339))
	if *dryRun {
		fmt.Fprintln(out, "Dry run: inputs are valid. No API request was made.")
		return nil
	}
	cache, err := openCache(*dbPath)
	if err != nil {
		return err
	}
	defer cache.Close()
	client := NewsClient{
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		Endpoint: "https://newsapi.org/v2/everything",
		APIKey:   os.Getenv("NEWS_API_KEY"),
	}
	service := SearchService{Cache: cache, Client: client}
	started := time.Now()
	result, err := service.Search(context.Background(), req)
	if err != nil {
		return err
	}
	articles := result.Articles
	fmt.Fprintf(out, "Returned: %d | Source: %s | API calls: %d | Elapsed: %s\n", len(articles), result.Source, result.APICalls, time.Since(started).Round(time.Millisecond))
	fmt.Fprintf(out, "Snapshot through: %s\n", result.SnapshotTo.Format(time.RFC3339))
	if len(articles) == 0 {
		fmt.Fprintln(out, "No articles are available for this search. The free NewsAPI plan delays articles by 24 hours.")
	}
	for i, article := range articles {
		fmt.Fprintf(out, "\n%d. %s\n   Source: %s | Published: %s\n   %s\n", i+1, article.Title, article.Source.Name, article.PublishedAt.Format(time.RFC3339), article.URL)
		if article.Description != "" {
			fmt.Fprintf(out, "   %s\n", article.Description)
		}
	}
	return nil
}
