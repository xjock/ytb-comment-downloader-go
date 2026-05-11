package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/xjock/ytb-comment-downloader-go/downloader"
)

func runServe(args []string) int {
	fs := flag.NewFlagSet("ytb-comment-downloader serve", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), `usage: ytb-comment-downloader serve [flags]

Run an HTTP API that streams YouTube comments as NDJSON.

Endpoints:
  GET  /health         Returns {"status":"ok"}
  GET  /comments       Query params: youtubeid|url, sort, language, limit
  POST /comments       JSON body: {youtubeid|url, sort, language, limit}

Flags:`)
		fs.PrintDefaults()
	}

	addr := fs.String("addr", ":8080", "listen address")
	readTimeout := fs.Duration("read-timeout", 10*time.Second, "request read timeout")
	writeTimeout := fs.Duration("write-timeout", 30*time.Minute, "response write timeout (long for streaming)")
	idleTimeout := fs.Duration("idle-timeout", 2*time.Minute, "keep-alive idle timeout")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /comments", handleComments)
	mux.HandleFunc("POST /comments", handleComments)

	srv := &http.Server{
		Addr:         *addr,
		Handler:      logRequests(mux),
		ReadTimeout:  *readTimeout,
		WriteTimeout: *writeTimeout,
		IdleTimeout:  *idleTimeout,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
			return 1
		}
		return 0
	case err, ok := <-errCh:
		if ok && err != nil {
			log.Printf("server error: %v", err)
			return 1
		}
		return 0
	}
}

type commentsRequest struct {
	YoutubeID string `json:"youtubeid"`
	URL       string `json:"url"`
	Sort      *int   `json:"sort,omitempty"`
	Language  string `json:"language"`
	Limit     int    `json:"limit"`
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func handleComments(w http.ResponseWriter, r *http.Request) {
	req, err := parseCommentsRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	target := req.URL
	if req.YoutubeID != "" {
		target = "https://www.youtube.com/watch?v=" + req.YoutubeID
	}
	if target == "" {
		writeJSONError(w, http.StatusBadRequest, "youtubeid or url is required")
		return
	}

	sort := downloader.SortByRecent
	if req.Sort != nil {
		sort = *req.Sort
	}

	dl, err := downloader.New()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	seq := dl.GetCommentsFromURL(r.Context(), target, downloader.Options{
		SortBy:   sort,
		Language: req.Language,
	})

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering for true streaming.

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	flusher, _ := w.(http.Flusher)

	count := 0
	headerSent := false
	for comment, err := range seq {
		if err != nil {
			// First-error special-case: emit a proper HTTP error so clients
			// can rely on the status code when nothing has been streamed yet.
			if !headerSent {
				writeIterError(w, err)
				return
			}
			// Mid-stream: surface the error as a final NDJSON line.
			_ = enc.Encode(map[string]string{"error": err.Error()})
			return
		}
		if req.Limit > 0 && count >= req.Limit {
			break
		}
		if !headerSent {
			w.WriteHeader(http.StatusOK)
			headerSent = true
		}
		if err := enc.Encode(comment); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		count++
	}

	if !headerSent {
		// No comments and no error — empty 200 body with NDJSON content type.
		w.WriteHeader(http.StatusOK)
	}
}

func parseCommentsRequest(r *http.Request) (commentsRequest, error) {
	var req commentsRequest

	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, http.ErrBodyReadAfterClose) {
			return req, fmt.Errorf("invalid JSON body: %w", err)
		}
	}

	q := r.URL.Query()
	if v := q.Get("youtubeid"); v != "" {
		req.YoutubeID = v
	}
	if v := q.Get("url"); v != "" {
		req.URL = v
	}
	if v := q.Get("language"); v != "" {
		req.Language = v
	}
	if v := q.Get("sort"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return req, fmt.Errorf("invalid sort: %w", err)
		}
		req.Sort = &n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return req, fmt.Errorf("invalid limit: %w", err)
		}
		req.Limit = n
	}
	return req, nil
}

func writeIterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, downloader.ErrCommentsDisabled):
		writeJSONError(w, http.StatusNotFound, "comments disabled or unavailable")
	case errors.Is(err, downloader.ErrConfigNotFound):
		writeJSONError(w, http.StatusBadGateway, "could not extract YouTube configuration")
	case errors.Is(err, downloader.ErrSortFailed):
		writeJSONError(w, http.StatusBadRequest, "invalid sort value for this video")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		writeJSONError(w, http.StatusRequestTimeout, err.Error())
	default:
		writeJSONError(w, http.StatusBadGateway, err.Error())
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.RequestURI(), time.Since(start))
	})
}
