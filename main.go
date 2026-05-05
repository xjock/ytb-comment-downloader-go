package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/luca/ytb-comment-downloader-go/downloader"
)

const indentWidth = 4

func main() {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		os.Exit(runServe(os.Args[2:]))
	}
	os.Exit(runCLI(os.Args[1:]))
}

func runCLI(args []string) int {
	fs := flag.NewFlagSet("ytb-comment-downloader", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `usage: ytb-comment-downloader [flags]
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

Run "ytb-comment-downloader serve --help" for HTTP server flags.`)
	}

	var (
		youtubeID, url, output, language string
		pretty                           bool
		limit                            int
		sort                             = downloader.SortByRecent
	)

	fs.StringVar(&youtubeID, "youtubeid", "", "YouTube video ID")
	fs.StringVar(&youtubeID, "y", "", "YouTube video ID (short)")
	fs.StringVar(&url, "url", "", "YouTube video URL")
	fs.StringVar(&url, "u", "", "YouTube video URL (short)")
	fs.StringVar(&output, "output", "", "Output filename")
	fs.StringVar(&output, "o", "", "Output filename (short)")
	fs.BoolVar(&pretty, "pretty", false, "Indented JSON output")
	fs.BoolVar(&pretty, "p", false, "Indented JSON output (short)")
	fs.IntVar(&limit, "limit", 0, "Maximum comments")
	fs.IntVar(&limit, "l", 0, "Maximum comments (short)")
	fs.StringVar(&language, "language", "", "UI language code")
	fs.StringVar(&language, "a", "", "UI language code (short)")
	fs.IntVar(&sort, "sort", downloader.SortByRecent, "0 = popular, 1 = recent")
	fs.IntVar(&sort, "s", downloader.SortByRecent, "0 = popular, 1 = recent (short)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if (youtubeID == "" && url == "") || output == "" {
		fs.Usage()
		fmt.Fprintln(os.Stderr, "\nError: --youtubeid/--url and --output are required")
		return 2
	}

	if dir := filepath.Dir(output); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 1
		}
	}

	target := url
	if youtubeID != "" {
		target = "https://www.youtube.com/watch?v=" + youtubeID
	}
	fmt.Println("Downloading YouTube comments for", firstNonEmpty(youtubeID, url))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	dl, err := downloader.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	opts := downloader.Options{SortBy: sort, Language: language}
	seq := dl.GetCommentsFromURL(ctx, target, opts)

	count, err := writeComments(output, seq, limit, pretty)
	if err != nil {
		// Comments-disabled and config-not-found are soft failures: nothing to
		// download, exit cleanly with a friendly note.
		if errors.Is(err, downloader.ErrCommentsDisabled) || errors.Is(err, downloader.ErrConfigNotFound) {
			fmt.Println("No comments available!")
			return 0
		}
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	if count == 0 {
		fmt.Println("No comments available!")
		return 0
	}
	fmt.Printf("\n[%.2f seconds] Done!\n", time.Since(downloadStartTime).Seconds())
	return 0
}

// downloadStartTime is set once at startup so the final report mirrors the
// Python "[X.XX seconds] Done!" message. Capturing it at package init keeps
// the CLI flow readable.
var downloadStartTime = time.Now()

// writeComments writes the iterator to disk in either NDJSON or pretty mode.
// It returns the number of comments written.
func writeComments(path string, seq iter.Seq2[downloader.Comment, error], limit int, pretty bool) (int, error) {
	var (
		fp        *os.File
		count     int
		closeFile = func() error { return nil }
	)
	defer func() { _ = closeFile() }()

	openLazy := func() (*os.File, error) {
		if fp != nil {
			return fp, nil
		}
		f, err := os.Create(path)
		if err != nil {
			return nil, err
		}
		fp = f
		closeFile = f.Close
		if pretty {
			if _, err := fmt.Fprintf(fp, "{\n%s\"comments\": [\n", strings.Repeat(" ", indentWidth)); err != nil {
				return nil, err
			}
		}
		return fp, nil
	}

	first := true
	for comment, err := range seq {
		if err != nil {
			return count, err
		}
		if limit > 0 && count >= limit {
			break
		}
		f, err := openLazy()
		if err != nil {
			return count, err
		}
		if pretty {
			if !first {
				if _, err := io.WriteString(f, ",\n"); err != nil {
					return count, err
				}
			}
			if err := writePrettyComment(f, comment); err != nil {
				return count, err
			}
		} else {
			if err := writeNDJSON(f, comment); err != nil {
				return count, err
			}
		}
		first = false
		count++
		fmt.Fprintf(os.Stdout, "Downloaded %d comment(s)\r", count)
	}

	if pretty && fp != nil {
		if _, err := fmt.Fprintf(fp, "\n%s]\n}", strings.Repeat(" ", indentWidth)); err != nil {
			return count, err
		}
	}
	return count, nil
}

func writeNDJSON(w io.Writer, c downloader.Comment) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(c)
}

func writePrettyComment(w io.Writer, c downloader.Comment) error {
	body, err := json.MarshalIndent(c, strings.Repeat(" ", 2*indentWidth), strings.Repeat(" ", indentWidth))
	if err != nil {
		return err
	}
	// MarshalIndent omits the prefix on the first line; the original Python
	// helper prefixes every line uniformly so the comment object lines up
	// inside the surrounding "comments" array.
	if _, err := io.WriteString(w, strings.Repeat(" ", 2*indentWidth)); err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
