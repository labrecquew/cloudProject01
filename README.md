# cloudProject01

A Go command-line program that searches news topics, caches results, and handles multiple users concurrently.
For MSCS 621 Cloud Computing, Fall 2026, Marist University.

## Requirements

Go 1.27.1, a NewsAPI key, and Docker Desktop using Linux containers.
Run the examples from the repository directory in PowerShell. `go` and `docker` must be on your PATH.

## Search for news

```powershell
$env:NEWS_API_KEY = "YOUR_KEY"
go run . -query "artificial intelligence" -days 2 -limit 5
```

`-query` is the topic, `-days` includes today (in UTC), and `-limit` is the maximum article count.
Two days means today and yesterday. NewsAPI account restrictions apply; its free plan delays articles.

Results are saved in `data/news.db`. Covered searches reuse the database; larger limits or uncovered
periods call the API. Same-day cache hits reuse the saved snapshot. Add `-dry-run` to check your inputs
without an API call.

## Multiple users at once

Place one request per line in a JSONL file, with a unique request ID:

```json
{"id":"alice-1","user":"alice","query":"artificial intelligence","days":2,"limit":5}
```

Run the included six-user example:

```powershell
go run . -batch testdata/requests.jsonl -workers 4
```

This runs four worker goroutines in one program, using jobs and results channels. Each response shows
its request ID and user. Output order can vary. Use batch mode for simultaneous users; only one
process can open a given database file at a time.

## Build and use the Docker image

Build from the Dockerfile and Go source:

```powershell
docker build --platform linux/amd64 -t cloudproject01:1.0 .
```

The build runs race tests and produces a small `scratch` image with the executable and HTTPS certificates.
Create a volume to keep the database between runs, then search:

```powershell
docker volume create cloudproject01-data
docker run --rm --mount source=cloudproject01-data,target=/data -e NEWS_API_KEY cloudproject01:1.0 -query "artificial intelligence" -days 2 -limit 5
```

For concurrent requests inside one container:

```powershell
Get-Content testdata/requests.jsonl | docker run --rm -i --mount source=cloudproject01-data,target=/data -e NEWS_API_KEY cloudproject01:1.0 -batch - -workers 4
```

To export or load the submitted image:

```powershell
docker save -o cloudproject01-image.tar cloudproject01:1.0
docker load -i cloudproject01-image.tar
```

## Repository and submission files

| Location | Purpose |
|---|---|
| `main.go`, `request.go` | CLI options, validation, and date ranges |
| `news.go` | NewsAPI requests and article handling |
| `cache.go` | Persistent database and cache rules |
| `workers.go` | Concurrent batch processing |
| `*_test.go`, `testdata/` | Tests, input files, and saved outputs |
| `data/` | Live cache databases created by the app; excluded from Git |
| `go.mod`, `go.sum` | Go dependency versions |
| `Dockerfile`, `.dockerignore` | Image build instructions and build-context exclusions |
| `docs/report.md` | Project report, design, test results, measurements, and AI prompts |
| `cloudproject01-image.tar` | Exported image in the local repository folder; excluded from Git |

The GitHub repository contains the source, Dockerfile, README, report, and test inputs/outputs
after they are committed and pushed. Submit the image archive separately. Use the repository
link for the source only if your instructor accepts links.
