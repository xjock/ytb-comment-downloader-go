# ytb-comment-downloader-go

A Go port of [youtube-comment-downloader](https://github.com/egbertbouman/youtube-comment-downloader).
Downloads YouTube comments without using the YouTube API. Output is line-delimited
JSON (NDJSON), or pretty-printed JSON with `--pretty`.

Single statically-linked binary, standard library only, ships as a CLI **and** an
HTTP service.

## Install

Build from source (requires Go 1.26+):

```sh
go install github.com/luca/ytb-comment-downloader-go@latest
```

Or clone and build with `make`:

```sh
git clone https://github.com/luca/ytb-comment-downloader-go.git
cd ytb-comment-downloader-go
make build
```

Cross-compile for all supported targets into `./dist/`:

```sh
make dist
```

## CLI

```
$ ytb-comment-downloader --help
usage: ytb-comment-downloader [flags]
       ytb-comment-downloader serve [flags]

Download YouTube comments without using the YouTube API.

Flags:
  --youtubeid, -y ID       YouTube video ID
  --url, -u URL            YouTube video URL (alternative to --youtubeid)
  --output, -o FILE        Output filename (line-delimited JSON by default)
  --pretty, -p             Format output as indented JSON
  --limit, -l N            Maximum number of comments to download
  --language, -a CODE      UI language for relative timestamps (e.g. en, de)
  --sort, -s 0|1           0 = popular, 1 = recent (default 1)
  --help, -h               Show this help

Run "ytb-comment-downloader serve --help" for HTTP server flags.
```

Examples:

```sh
ytb-comment-downloader --url https://www.youtube.com/watch?v=ScMzIvxBSi4 \
                       --output ScMzIvxBSi4.json

ytb-comment-downloader --youtubeid ScMzIvxBSi4 --output ScMzIvxBSi4.json

ytb-comment-downloader -y ScMzIvxBSi4 -o popular.json --sort 0 --limit 100 --pretty
```

For YouTube IDs starting with a dash, use `=`:

```sh
ytb-comment-downloader --youtubeid=-idwithdash --output out.json
```

## HTTP API

Start the server:

```sh
ytb-comment-downloader serve --addr=:8080
```

Endpoints:

| Method | Path        | Description                                  |
| ------ | ----------- | -------------------------------------------- |
| GET    | `/health`   | `{"status":"ok"}`                            |
| GET    | `/comments` | Stream comments as NDJSON (query parameters) |
| POST   | `/comments` | Stream comments as NDJSON (JSON body)        |

`/comments` parameters (query string or JSON body):

| Field       | Type    | Default | Description                       |
| ----------- | ------- | ------- | --------------------------------- |
| `youtubeid` | string  | —       | YouTube video ID                  |
| `url`       | string  | —       | YouTube video URL (alternative)   |
| `sort`      | int     | `1`     | `0` popular, `1` recent           |
| `language`  | string  | —       | UI language code, e.g. `en`, `de` |
| `limit`     | int     | `0`     | Max comments (`0` = unlimited)    |

Examples:

```sh
# GET with query params
curl -N "http://localhost:8080/comments?youtubeid=ScMzIvxBSi4&limit=5"

# POST with JSON body
curl -N -X POST http://localhost:8080/comments \
     -H 'Content-Type: application/json' \
     -d '{"youtubeid":"ScMzIvxBSi4","sort":0,"limit":10}'

# Pipe NDJSON into jq for ad-hoc filtering
curl -sN "http://localhost:8080/comments?youtubeid=ScMzIvxBSi4&limit=20" \
     | jq -r '.author + ": " + .text'
```

The response is `Content-Type: application/x-ndjson`: one JSON object per line,
flushed as the upstream stream produces it. If an error occurs **before** any
comment has been written the server returns an HTTP error status and a JSON
error body. If an error occurs **mid-stream**, the connection is left open and
a final line `{"error":"..."}` is appended.

`serve` flags:

```
--addr           listen address (default ":8080")
--read-timeout   request read timeout (default 10s)
--write-timeout  response write timeout, long for streaming (default 30m)
--idle-timeout   keep-alive idle timeout (default 2m)
```

## Comment schema

```json
{
  "cid": "Ugxxxxxxxxxxxxxxxxx",
  "text": "Some comment body",
  "time": "2 days ago",
  "author": "@SomeChannel",
  "channel": "UCxxxxxxxxxxxxxx",
  "votes": "12",
  "replies": "3",
  "photo": "https://yt3.ggpht.com/...",
  "heart": false,
  "reply": false,
  "time_parsed": 1714867200.0,
  "paid": "$2.00"
}
```

`time_parsed` is a Unix timestamp derived from `time`. `paid` is only present
for Super Chats. `reply` is `true` for replies (cid contains `.`).

## Docker

Multi-stage build, runs as `nonroot` on `gcr.io/distroless/static-debian12`:

```sh
make docker                                 # builds ytb-comment-downloader:latest
docker run --rm -p 8080:8080 ytb-comment-downloader:latest
curl http://localhost:8080/health
```

CLI mode in Docker:

```sh
docker run --rm -v "$PWD:/data" ytb-comment-downloader:latest \
       --youtubeid ScMzIvxBSi4 --output /data/out.json
```

## Library use

```go
package main

import (
    "context"
    "fmt"

    "github.com/luca/ytb-comment-downloader-go/downloader"
)

func main() {
    dl, err := downloader.New()
    if err != nil { panic(err) }

    ctx := context.Background()
    seq := dl.GetComments(ctx, "ScMzIvxBSi4", downloader.Options{
        SortBy: downloader.SortByPopular,
    })

    n := 0
    for c, err := range seq {
        if err != nil { panic(err) }
        fmt.Printf("%s: %s\n", c.Author, c.Text)
        if n++; n >= 10 { break }
    }
}
```

## Development

```sh
make test     # run unit tests
make vet      # go vet
make fmt      # gofmt -w
make clean    # remove built artifacts
```

## License

MIT — see [LICENSE](LICENSE). The original Python implementation is
copyright 2015 Egbert Bouman.
