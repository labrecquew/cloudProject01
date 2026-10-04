# cloudProject01 Report

MSCS 621 Cloud Computing, Fall 2026, Marist University

## Project overview

This project is a Go command-line application for searching news by topic, number of days,
and maximum article count. It uses NewsAPI, saves results in a bbolt database, and supports
multiple users through concurrent batch requests. Two days means today and yesterday in UTC.

## Design

- **Go CLI:** a simple interface that accepts command-line options or a JSONL batch file.
- **NewsAPI:** provides articles, publication times, sources, descriptions, and links.
- **bbolt:** stores articles and search coverage in one local file without a database server.
- **Concurrency:** worker goroutines receive jobs through a channel and send results through
  another channel. One collector prints each response with its user and request ID.
- **Docker:** a multi-stage build compiles and tests the code, then copies only the stripped
  executable and HTTPS certificates into a `scratch` image.

Repeated searches use the database when it covers the requested days and limit. Larger limits
or uncovered periods call the API and update the database. Articles are deduplicated by URL.
Same-day cached results are snapshots; a new UTC day uses a new search key. Requests from
users searching the same topic share the cache, with locks preventing redundant simultaneous
API calls. Different topics can run concurrently.

## Build and run

From the repository directory in PowerShell, compile and run a search:

```powershell
go build -o cloudProject01.exe .
$env:NEWS_API_KEY = "YOUR_KEY"
.\cloudProject01.exe -query "artificial intelligence" -days 2 -limit 5
```

Run the six-user example with four workers:

```powershell
go run . -batch testdata/requests.jsonl -workers 4
```

Build the Linux Docker image from the source and Dockerfile:

```powershell
docker build --platform linux/amd64 -t cloudproject01:1.0 .
```

Docker run, volume, and batch commands are in the README. A named volume keeps the database
between container runs. The API key is supplied at runtime and is not part of the image.

## Testing and results

All 18 automated tests and `go vet` passed. Linux race tests also passed during the Docker build.
Tests cover input validation, UTC date ranges, pagination, duplicate articles, API errors,
cache reuse, larger searches, empty results, database restarts, and concurrent requests.
A 120-request test returned one response per request, and four independent topics were
confirmed to start before any of their simulated API calls finished.

Live checks confirmed that repeated and smaller searches used the database, while larger
limits and periods called the API. Docker checks confirmed HTTPS access and persistent
caching across separate containers.

| Docker check | API calls | Processing time |
|---|---:|---:|
| First single search, 5 articles | 1 | 733 ms |
| Repeated single search | 0 | 1 ms |
| First batch, 6 users and 4 workers | 3 | 193 ms |
| Repeated batch without an API key | 0 | 1 ms |

These are individual observed runs. Processing times exclude compilation and container startup.
Test inputs and saved outputs are in `testdata/`. The exported image was loaded successfully,
and the packaged source was rebuilt successfully.

The verified Linux/amd64 Docker image is **10,334,114 bytes (9.86 MiB)**. The exported
`cloudproject01-image.tar` is **3,135,488 bytes**; archive size differs from image size.
The archive is in the local repository folder and is excluded from Git. The source,
Dockerfile, README, this report, and test inputs/outputs are included in the GitHub repository
once committed and pushed.

## AI assistance

**Tool:** OpenAI Codex desktop app  
**Model:** GPT-6.1 Sol

We worked back and forth through the generated plan, completing and testing each milestone.
I ran commands and shared outputs to verify behavior before moving on. AI also helped with
Git explanations and documentation. Over my internship in the summer, they pushed us to
use RIC prompts a lot, so that was my primary form of making the prompt before starting
a back and forth. Prompt 1 was my bread and butter, it helped lay out my whole project for me.
Prompt 2 was just very useful to summarize everything that has happened, where I then make my own
edits like I am doing right now.

### Prompt 1: Project planning

The two RIC prompts are rewritten summaries of the requests, rather than verbatim copies
of the conversation.

**Role:** Act as a practical Go and Docker mentor who explains each step clearly.

**Instructions:** Create a complete, step-by-step plan that follows the instructor's project
requirements. Keep the design simple and prioritize a small Docker image with all required
code working. Include setup, implementation, caching, concurrent requests, testing,
documentation, and submission. Leave extra credit until the initial assignment is complete.

**Context:** The project repository is named `cloudProject01`. A GitHub remote has been
created and cloned locally. The assignment requires a Go news-search application, an API,
persistent database caching, goroutines and channels, and a Docker image. Image size affects
the grade, and the plan will guide the implementation. Attached file has full details (this was
the assignment pdf)

### Prompt 2: Professional documentation

**Role:** Act as a technical editor who writes clear, concise project documentation.

**Instructions:** Make the README and report professional and easy to follow. Keep the README
focused on using the application, concurrent requests, building the Docker image, and a brief
repository structure. Keep the report focused on the design, testing, results, image size,
and AI assistance. Avoid unnecessary complexity and lengthy explanations.

**Context:** The application has been implemented and tested. The source, Dockerfile, test
inputs, saved outputs, and exported image already exist. The documents should help a reader
understand the project and reproduce its build and usage.
