# gitGost [BETA Project]

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue.svg)](https://golang.org/)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Report Card](https://goreportcard.com/badge/github.com/livrasand/gitGost)](https://goreportcard.com/report/github.com/livrasand/gitGost)
[![CI](https://github.com/livrasand/gitGost/workflows/CI/badge.svg)](https://github.com/livrasand/gitGost/actions)

A Go server that allows anonymous Git contributions to GitHub repositories by receiving pushes, anonymizing commits, and creating pull requests.

## Description

gitGost acts as a Git remote that accepts pushes from users, processes the commits to remove author identity, pushes the anonymized changes to a GitHub repository, and automatically creates a pull request.

## Features

- Git Smart HTTP receive-pack support
- Commit anonymization (squash to single anonymous commit)
- Automatic push to GitHub with unique branch names
- Pull request creation via GitHub API
- Security features: size limits, input validation
- Automatic cleanup of temporary directories

## Configuration

gitGost can be configured using environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | Port to run the server on |
| `READ_TIMEOUT` | `30s` | HTTP read timeout |
| `WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `GITHUB_TOKEN` | *required* | GitHub personal access token with repo permissions |
| `GITGOST_API_KEY` | *optional* | API key for authentication |
| `LOG_FORMAT` | `text` | Log format: `text` or `json` |

## Usage

### Local Development

1. Set the required environment variables.

2. Run the server:
    ```bash
    go run cmd/server/main.go
    ```

### Docker

Build and run with Docker:

```bash
# Build the image
docker build -t gitgost .

# Run the container
docker run -p 8080:8080 \
  -e GITHUB_TOKEN=your_github_token \
  -e GITGOST_API_KEY=your_api_key \
  gitgost
```

Or use docker-compose:

```yaml
version: '3.8'
services:
  gitgost:
    build: .
    ports:
      - "8080:8080"
    environment:
      - GITHUB_TOKEN=your_github_token
      - GITGOST_API_KEY=your_api_key
    restart: unless-stopped
```

3. From a Git repository, add gitGost as a remote:
   ```bash
   git remote add gitgost https://gitgost.leapcell.app/v1/gh/owner/repo
   ```

4. Push to the remote:
   ```bash
   git push gitgost your-branch:main
   ```

The server will:
- Receive the push
- Create an anonymous commit
- Push to a new branch on GitHub (e.g., `gitgost/pr-1234567890-123`)
- Create a pull request titled "Anonymous contribution (via gitGost)"

## API

### Authentication

If `GITGOST_API_KEY` is set, all API requests must include the header:
```
X-Gitgost-Key: your-api-key
```

### POST /v1/gh/:owner/:repo/git-receive-pack

Receives Git push data and processes it anonymously.

**Parameters:**
- `owner`: GitHub repository owner
- `repo`: GitHub repository name

**Request Body:** Git packfile data

**Response:**
```json
{
  "pr_url": "https://github.com/owner/repo/pull/123",
  "branch": "gitgost/pr-1234567890-123",
  "status": "ok"
}
```

### GET /health

Health check endpoint.

**Response:**
```json
{
  "status": "healthy",
  "time": "2025-11-29T20:39:17Z"
}
```

### GET /metrics

Basic system metrics.

**Response:**
```json
{
  "memory": {
    "alloc": 1048576,
    "total_alloc": 2097152,
    "sys": 4194304,
    "num_gc": 5
  },
  "goroutines": 8,
  "uptime": "1h30m45s"
}
```

## Security

- Size limit: 100MB per push
- Input validation for owner/repo names
- No path traversal allowed
