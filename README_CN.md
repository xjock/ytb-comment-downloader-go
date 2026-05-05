# ytb-comment-downloader-go

[youtube-comment-downloader](https://github.com/egbertbouman/youtube-comment-downloader) 的 Go 语言移植版。
无需使用 YouTube API 即可下载 YouTube 评论。默认输出为换行分隔的 JSON（NDJSON），
使用 `--pretty` 可输出格式化后的 JSON。

单个静态链接二进制文件，仅使用标准库，同时提供 **CLI** 和 **HTTP 服务** 两种使用方式。

## 安装

从源码构建（需要 Go 1.26+）：

```sh
go install github.com/luca/ytb-comment-downloader-go@latest
```

或者克隆仓库并使用 `make` 构建：

```sh
git clone https://github.com/luca/ytb-comment-downloader-go.git
cd ytb-comment-downloader-go
make build
```

为所有支持的目标平台交叉编译，输出到 `./dist/`：

```sh
make dist
```

## 命令行使用

```
$ ytb-comment-downloader --help
usage: ytb-comment-downloader [flags]
       ytb-comment-downloader serve [flags]

无需使用 YouTube API 下载 YouTube 评论。

Flags:
  --youtubeid, -y ID       YouTube 视频 ID
  --url, -u URL            YouTube 视频 URL（--youtubeid 的替代方案）
  --output, -o FILE        输出文件名（默认换行分隔 JSON）
  --pretty, -p             输出缩进格式的 JSON
  --limit, -l N            最多下载的评论数量
  --language, -a CODE      相对时间戳的界面语言（例如 en, de）
  --sort, -s 0|1           0 = 热门排序, 1 = 最新排序（默认 1）
  --help, -h               显示帮助信息

运行 "ytb-comment-downloader serve --help" 查看 HTTP 服务器相关参数。
```

示例：

```sh
ytb-comment-downloader --url https://www.youtube.com/watch?v=ScMzIvxBSi4 \
                       --output ScMzIvxBSi4.json

ytb-comment-downloader --youtubeid ScMzIvxBSi4 --output ScMzIvxBSi4.json

ytb-comment-downloader -y ScMzIvxBSi4 -o popular.json --sort 0 --limit 100 --pretty
```

如果 YouTube ID 以短横线开头，请使用 `=`：

```sh
ytb-comment-downloader --youtubeid=-idwithdash --output out.json
```

## HTTP API

启动服务：

```sh
ytb-comment-downloader serve --addr=:8080
```

接口：

| 方法 | 路径        | 说明                                  |
| ---- | ----------- | ------------------------------------- |
| GET  | `/health`   | `{"status":"ok"}`                   |
| GET  | `/comments` | 以 NDJSON 流式输出评论（URL 参数）    |
| POST | `/comments` | 以 NDJSON 流式输出评论（JSON 请求体） |

`/comments` 参数（URL 参数或 JSON 请求体）：

| 字段        | 类型   | 默认值 | 说明                              |
| ----------- | ------ | ------ | --------------------------------- |
| `youtubeid` | string | —      | YouTube 视频 ID                   |
| `url`       | string | —      | YouTube 视频 URL（替代方案）      |
| `sort`      | int    | `1`    | `0` 热门, `1` 最新                |
| `language`  | string | —      | 界面语言代码，例如 `en`, `de`     |
| `limit`     | int    | `0`    | 最大评论数（`0` 表示无限制）      |

示例：

```sh
# GET 请求，使用 URL 参数
curl -N "http://localhost:8080/comments?youtubeid=ScMzIvxBSi4&limit=5"

# POST 请求，使用 JSON 请求体
curl -N -X POST http://localhost:8080/comments \
     -H 'Content-Type: application/json' \
     -d '{"youtubeid":"ScMzIvxBSi4","sort":0,"limit":10}'

# 将 NDJSON 通过管道传入 jq 进行临时过滤
curl -sN "http://localhost:8080/comments?youtubeid=ScMzIvxBSi4&limit=20" \
     | jq -r '.author + ": " + .text'
```

响应的 `Content-Type` 为 `application/x-ndjson`：每行一个 JSON 对象，
随上游数据流实时刷新。如果在写入任何评论**之前**发生错误，
服务器会返回 HTTP 错误状态码和 JSON 错误体。如果在**流式传输中途**发生错误，
连接会保持打开状态，并在最后一行追加 `{"error":"..."}`。

`serve` 参数：

```
--addr           监听地址（默认 ":8080"）
--read-timeout   请求读取超时（默认 10s）
--write-timeout  响应写入超时，流式传输建议设长（默认 30m）
--idle-timeout   保持连接空闲超时（默认 2m）
```

## 评论数据结构

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

`time_parsed` 是从 `time` 解析出的 Unix 时间戳。`paid` 仅对超级留言（Super Chat）存在。
`reply` 对回复评论为 `true`（cid 包含 `.`）。

## Docker

多阶段构建，在 `gcr.io/distroless/static-debian12` 上以 `nonroot` 用户运行：

```sh
make docker                                 # 构建 ytb-comment-downloader:latest
docker run --rm -p 8080:8080 ytb-comment-downloader:latest
curl http://localhost:8080/health
```

Docker 中运行 CLI 模式：

```sh
docker run --rm -v "$PWD:/data" ytb-comment-downloader:latest \
       --youtubeid ScMzIvxBSi4 --output /data/out.json
```

## 作为库使用

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

## 开发

```sh
make test     # 运行单元测试
make vet      # go vet
make fmt      # gofmt -w
make clean    # 删除构建产物
```

## 许可证

MIT — 详见 [LICENSE](LICENSE)。原始 Python 实现版权所有 2015 Egbert Bouman。
