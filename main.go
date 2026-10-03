package main

import (
	"bytes"
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
	return runWithInput(args, os.Stdin, out, errOut)
}

func runWithInput(args []string, in io.Reader, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("cloudProject01", flag.ContinueOnError)
	flags.SetOutput(errOut)
	query := flags.String("query", "", "news topic (required)")
	days := flags.Int("days", 1, "UTC calendar days, including today")
	limit := flags.Int("limit", 20, "maximum number of articles")
	dbPath := flags.String("db", "data/news.db", "persistent database file")
	batch := flags.String("batch", "", "JSONL request file, or - for standard input")
	workers := flags.Int("workers", 8, "number of concurrent batch workers")
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
	if *workers < 1 {
		return fmt.Errorf("workers must be a positive integer")
	}
	var requests []BatchRequest
	if *batch != "" {
		conflict := false
		flags.Visit(func(f *flag.Flag) {
			if f.Name == "query" || f.Name == "days" || f.Name == "limit" {
				conflict = true
			}
		})
		if conflict {
			return fmt.Errorf("batch mode reads query, days, and limit from the file; do not also specify those flags")
		}
		if *batch != "-" {
			file, err := os.Open(*batch)
			if err != nil {
				return fmt.Errorf("open batch file: %w", err)
			}
			defer file.Close()
			in = file
		}
		var err error
		requests, err = readBatch(in, time.Now())
		if err != nil {
			return err
		}
		if *dryRun {
			for _, request := range requests {
				fmt.Fprintf(out, "\nRequest: %s | User: %s\n", request.ID, request.User)
				writeRequest(out, request.Search)
			}
			_, err := fmt.Fprintf(out, "\nDry run: %d valid requests. No API request was made.\n", len(requests))
			return err
		}
	}
	var req SearchRequest
	if *batch == "" {
		var err error
		req, err = newSearchRequest(*query, *days, *limit, time.Now())
		if err != nil {
			return err
		}
		writeRequest(out, req)
		if *dryRun {
			fmt.Fprintln(out, "Dry run: inputs are valid. No API request was made.")
			return nil
		}
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
	if *batch != "" {
		return processBatch(context.Background(), requests, *workers, &service, out)
	}
	started := time.Now()
	result, err := service.Search(context.Background(), req)
	if err != nil {
		return err
	}
	return writeResult(out, result, time.Since(started))
}

func writeRequest(out io.Writer, req SearchRequest) {
	fmt.Fprintf(out, "Topic: %s\nDays: %d | Maximum articles: %d\nUTC range: %s to %s\n",
		req.Query, req.Days, req.Limit, req.From.Format(time.RFC3339), req.To.Format(time.RFC3339))
}

func writeResult(out io.Writer, result SearchResult, elapsed time.Duration) error {
	var block bytes.Buffer
	articles := result.Articles
	fmt.Fprintf(&block, "Returned: %d | Source: %s | API calls: %d | Elapsed: %s\n", len(articles), result.Source, result.APICalls, elapsed.Round(time.Millisecond))
	fmt.Fprintf(&block, "Snapshot through: %s\n", result.SnapshotTo.Format(time.RFC3339))
	if len(articles) == 0 {
		fmt.Fprintln(&block, "No articles are available for this search. The free NewsAPI plan delays articles by 24 hours.")
	}
	for i, article := range articles {
		fmt.Fprintf(&block, "\n%d. %s\n   Source: %s | Published: %s\n   %s\n", i+1, article.Title, article.Source.Name, article.PublishedAt.Format(time.RFC3339), article.URL)
		if article.Description != "" {
			fmt.Fprintf(&block, "   %s\n", article.Description)
		}
	}
	_, err := out.Write(block.Bytes())
	return err
}
