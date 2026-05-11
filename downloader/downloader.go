package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Sort options accepted by the YouTube watch page.
const (
	SortByPopular = 0
	SortByRecent  = 1
)

const (
	youtubeVideoURL   = "https://www.youtube.com/watch?v=%s"
	youtubeConsentURL = "https://consent.youtube.com/save"

	defaultUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.130 Safari/537.36"
	defaultSleep      = 100 * time.Millisecond
	defaultRetries    = 5
	defaultRetrySleep = 20 * time.Second
	defaultTimeout    = 60 * time.Second
)

var (
	ytCfgRe         = regexp.MustCompile(`ytcfg\.set\s*\(\s*(\{.+?\})\s*\)\s*;`)
	ytInitialDataRe = regexp.MustCompile(`(?:window\s*\[\s*["']ytInitialData["']\s*\]|ytInitialData)\s*=\s*(\{.+?\})\s*;\s*(?:var\s+meta|</script|\n)`)
	ytHiddenInputRe = regexp.MustCompile(`<input\s+type="hidden"\s+name="([A-Za-z0-9_]+)"\s+value="([A-Za-z0-9_\-.]*)"\s*(?:required|)\s*>`)
)

// Comment is one rendered YouTube comment. JSON tags match the Python output
// so existing tooling that consumes the upstream JSON keeps working.
type Comment struct {
	Cid        string   `json:"cid"`
	Text       string   `json:"text"`
	Time       string   `json:"time"`
	Author     string   `json:"author"`
	Channel    string   `json:"channel"`
	Votes      string   `json:"votes"`
	Replies    string   `json:"replies"`
	Photo      string   `json:"photo"`
	Heart      bool     `json:"heart"`
	Reply      bool     `json:"reply"`
	TimeParsed *float64 `json:"time_parsed,omitempty"`
	Paid       string   `json:"paid,omitempty"`
}

// Options tunes a single download. Zero values fall back to sensible
// defaults; SortBy defaults to SortByRecent.
type Options struct {
	SortBy   int           // 0 = popular, 1 = recent.
	Language string        // Two-letter language code (e.g. "en"). Empty keeps YouTube's default.
	Sleep    time.Duration // Pause between paginated AJAX requests.
}

// Downloader fetches comments for a YouTube video. The zero value is not
// usable — call New().
type Downloader struct {
	client *http.Client
}

// New returns a Downloader with a fresh cookie jar and the consent cookie
// already populated.
func New() (*Downloader, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("downloader: cookie jar: %w", err)
	}
	yt, _ := url.Parse("https://www.youtube.com")
	jar.SetCookies(yt, []*http.Cookie{{
		Name:   "CONSENT",
		Value:  "YES+cb",
		Domain: "youtube.com",
		Path:   "/",
	}})
	return &Downloader{
		client: &http.Client{
			Transport: &userAgentTransport{base: http.DefaultTransport},
			Jar:       jar,
			Timeout:   defaultTimeout,
		},
	}, nil
}

type userAgentTransport struct{ base http.RoundTripper }

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultUserAgent)
	}
	return t.base.RoundTrip(req)
}

// GetComments fetches comments for the given video ID. The returned iterator
// yields comments lazily; consumers can break to stop early. Errors abort
// iteration with the second value populated.
func (d *Downloader) GetComments(ctx context.Context, youtubeID string, opts Options) iter.Seq2[Comment, error] {
	return d.GetCommentsFromURL(ctx, fmt.Sprintf(youtubeVideoURL, youtubeID), opts)
}

// GetCommentsFromURL is the URL-typed counterpart to GetComments.
func (d *Downloader) GetCommentsFromURL(ctx context.Context, youtubeURL string, opts Options) iter.Seq2[Comment, error] {
	if opts.Sleep == 0 {
		opts.Sleep = defaultSleep
	}
	return func(yield func(Comment, error) bool) {
		d.run(ctx, youtubeURL, opts, yield)
	}
}

// ErrCommentsDisabled is returned when YouTube exposes no continuation token
// for the video — usually because comments are disabled.
var ErrCommentsDisabled = errors.New("downloader: comments disabled or unavailable")

// ErrConfigNotFound means we could not extract ytcfg from the watch page.
var ErrConfigNotFound = errors.New("downloader: failed to extract ytcfg")

// ErrSortFailed means the chosen sort filter could not be applied.
var ErrSortFailed = errors.New("downloader: failed to set sorting")

func (d *Downloader) run(ctx context.Context, youtubeURL string, opts Options, yield func(Comment, error) bool) {
	html, err := d.fetchWatchPage(ctx, youtubeURL)
	if err != nil {
		yield(Comment{}, err)
		return
	}

	ytcfg, err := decodeJSONMatch(ytCfgRe, html)
	if err != nil {
		yield(Comment{}, ErrConfigNotFound)
		return
	}
	if opts.Language != "" {
		if client, ok := mapPath[map[string]any](ytcfg, "INNERTUBE_CONTEXT", "client"); ok {
			client["hl"] = opts.Language
		}
	}

	data, err := decodeJSONMatch(ytInitialDataRe, html)
	if err != nil {
		yield(Comment{}, fmt.Errorf("downloader: failed to extract initial data: %w", err))
		return
	}

	itemSection, ok := SearchDictFirst(data, "itemSectionRenderer")
	if !ok {
		yield(Comment{}, ErrCommentsDisabled)
		return
	}
	if _, ok := SearchDictFirst(itemSection, "continuationItemRenderer"); !ok {
		yield(Comment{}, ErrCommentsDisabled)
		return
	}

	sortMenu := extractSortMenu(data)
	if len(sortMenu) == 0 {
		// Maybe a community post — chase its continuation once and retry.
		sectionList, _ := SearchDictFirst(data, "sectionListRenderer")
		var firstCont any
		for ep := range SearchDict(sectionList, "continuationEndpoint") {
			firstCont = ep
			break
		}
		if firstCont != nil {
			retry, err := d.ajaxRequest(ctx, firstCont, ytcfg)
			if err != nil {
				yield(Comment{}, err)
				return
			}
			sortMenu = extractSortMenu(retry)
		}
	}
	if len(sortMenu) == 0 || opts.SortBy >= len(sortMenu) {
		yield(Comment{}, ErrSortFailed)
		return
	}

	chosen, ok := mapPath[any](sortMenu[opts.SortBy], "serviceEndpoint")
	if !ok {
		yield(Comment{}, ErrSortFailed)
		return
	}
	continuations := []any{chosen}

	for len(continuations) > 0 {
		select {
		case <-ctx.Done():
			yield(Comment{}, ctx.Err())
			return
		default:
		}

		// Pop from end (matches Python's stack semantics).
		n := len(continuations) - 1
		cont := continuations[n]
		continuations = continuations[:n]

		response, err := d.ajaxRequest(ctx, cont, ytcfg)
		if err != nil {
			yield(Comment{}, err)
			return
		}
		if len(response) == 0 {
			break
		}

		if errMsg, ok := SearchDictFirst(response, "externalErrorMessage"); ok {
			if s, _ := errMsg.(string); s != "" {
				yield(Comment{}, fmt.Errorf("downloader: server error: %s", s))
				return
			}
		}

		// Collect new continuations and surface payments before yielding comments,
		// matching the Python ordering precisely.
		var actions []any
		for a := range SearchDict(response, "reloadContinuationItemsCommand") {
			actions = append(actions, a)
		}
		for a := range SearchDict(response, "appendContinuationItemsAction") {
			actions = append(actions, a)
		}

		for _, action := range actions {
			actionMap, _ := action.(map[string]any)
			targetID, _ := actionMap["targetId"].(string)
			items, _ := actionMap["continuationItems"].([]any)
			for _, item := range items {
				switch targetID {
				case "comments-section",
					"engagement-panel-comments-section",
					"shorts-engagement-panel-comments-section":
					var found []any
					for ep := range SearchDict(item, "continuationEndpoint") {
						found = append(found, ep)
					}
					// Python prepends with `continuations[:0] = [...]` — preserve order.
					continuations = append(found, continuations...)
				default:
					if strings.HasPrefix(targetID, "comment-replies-item") {
						if _, ok := mapPath[any](item, "continuationItemRenderer"); ok {
							if br, ok := SearchDictFirst(item, "buttonRenderer"); ok {
								if cmd, ok := mapPath[any](br, "command"); ok {
									continuations = append(continuations, cmd)
								}
							}
						}
					}
				}
			}
		}

		payments := extractPayments(response)
		toolbarStates := map[string]map[string]any{}
		for tb := range SearchDict(response, "engagementToolbarStateEntityPayload") {
			if m, ok := tb.(map[string]any); ok {
				if k, ok := m["key"].(string); ok {
					toolbarStates[k] = m
				}
			}
		}

		// Yield in reversed order (Python does `reversed(list(...))`).
		var raw []any
		for c := range SearchDict(response, "commentEntityPayload") {
			raw = append(raw, c)
		}
		for i := len(raw) - 1; i >= 0; i-- {
			comment, ok := buildComment(raw[i], payments, toolbarStates)
			if !ok {
				continue
			}
			if !yield(comment, nil) {
				return
			}
		}

		select {
		case <-ctx.Done():
			yield(Comment{}, ctx.Err())
			return
		case <-time.After(opts.Sleep):
		}
	}
}

func (d *Downloader) fetchWatchPage(ctx context.Context, youtubeURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, youtubeURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloader: GET watch page: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("downloader: read watch body: %w", err)
	}

	if strings.Contains(resp.Request.URL.String(), "consent") {
		return d.handleConsent(ctx, youtubeURL, string(body))
	}
	return string(body), nil
}

func (d *Downloader) handleConsent(ctx context.Context, youtubeURL, html string) (string, error) {
	params := url.Values{}
	for _, m := range ytHiddenInputRe.FindAllStringSubmatch(html, -1) {
		params.Set(m[1], m[2])
	}
	params.Set("continue", youtubeURL)
	params.Set("set_eom", "false")
	params.Set("set_ytc", "true")
	params.Set("set_apyt", "true")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, youtubeConsentURL+"?"+params.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("downloader: consent POST: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("downloader: read consent body: %w", err)
	}
	return string(body), nil
}

func (d *Downloader) ajaxRequest(ctx context.Context, endpoint, ytcfg any) (map[string]any, error) {
	apiURL, ok := mapPath[string](endpoint, "commandMetadata", "webCommandMetadata", "apiUrl")
	if !ok {
		// YouTube sometimes omits apiUrl; fall back to url field or the
		// standard comments endpoint.
		apiURL, ok = mapPath[string](endpoint, "commandMetadata", "webCommandMetadata", "url")
		if !ok {
			apiURL = "/youtubei/v1/next"
		}
	}
	token, ok := mapPath[string](endpoint, "continuationCommand", "token")
	if !ok {
		// Some endpoints place the token directly on the endpoint object.
		token, ok = mapPath[string](endpoint, "token")
		if !ok {
			return nil, errors.New("downloader: continuation endpoint missing token")
		}
	}

	innerCtx, _ := mapPath[any](ytcfg, "INNERTUBE_CONTEXT")
	apiKey, _ := mapPath[string](ytcfg, "INNERTUBE_API_KEY")

	body, err := json.Marshal(map[string]any{
		"context":      innerCtx,
		"continuation": token,
	})
	if err != nil {
		return nil, fmt.Errorf("downloader: marshal continuation body: %w", err)
	}

	requestURL := "https://www.youtube.com" + apiURL
	if apiKey != "" {
		requestURL += "?key=" + url.QueryEscape(apiKey)
	}

	for range defaultRetries {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.client.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			// Treat all transport errors as retryable (matches Python's broad `except Timeout`).
		} else {
			data, status, decodeErr := drainAndDecode(resp)
			switch {
			case decodeErr != nil && status == http.StatusOK:
				return nil, decodeErr
			case status == http.StatusOK:
				return data, nil
			case status == http.StatusForbidden, status == http.StatusRequestEntityTooLarge:
				return map[string]any{}, nil
			}
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(defaultRetrySleep):
		}
	}
	return nil, nil
}

func drainAndDecode(resp *http.Response) (map[string]any, int, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, resp.StatusCode, nil
	}
	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("downloader: decode continuation: %w", err)
	}
	return data, resp.StatusCode, nil
}

func decodeJSONMatch(re *regexp.Regexp, text string) (map[string]any, error) {
	m := re.FindStringSubmatch(text)
	if len(m) < 2 {
		return nil, errors.New("regex did not match")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(m[1]), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func extractSortMenu(data any) []map[string]any {
	for root := range SearchDict(data, "sortFilterSubMenuRenderer") {
		items, ok := mapPath[[]any](root, "subMenuItems")
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			if m, ok := it.(map[string]any); ok {
				if _, hasSE := m["serviceEndpoint"]; hasSE {
					out = append(out, m)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func extractPayments(response any) map[string]string {
	type viewModelKey struct {
		surfaceKey string
		commentID  string
	}

	rawPayments := map[string]string{}
	for payload := range SearchDict(response, "commentSurfaceEntityPayload") {
		m, ok := payload.(map[string]any)
		if !ok {
			continue
		}
		if _, ok := m["pdgCommentChip"]; !ok {
			continue
		}
		key, _ := m["key"].(string)
		if key == "" {
			continue
		}
		text, _ := SearchDictFirst(m, "simpleText")
		if s, ok := text.(string); ok {
			rawPayments[key] = s
		}
	}
	if len(rawPayments) == 0 {
		return nil
	}

	surfaceKeys := map[string]string{}
	for vm := range SearchDict(response, "commentViewModel") {
		inner, _ := mapPath[map[string]any](vm, "commentViewModel")
		if inner == nil {
			continue
		}
		sk, _ := inner["commentSurfaceKey"].(string)
		cid, _ := inner["commentId"].(string)
		if sk != "" && cid != "" {
			surfaceKeys[sk] = cid
		}
	}

	resolved := make(map[string]string, len(rawPayments))
	for key, val := range rawPayments {
		if cid, ok := surfaceKeys[key]; ok {
			resolved[cid] = val
		}
	}
	return resolved
}

func buildComment(raw any, payments map[string]string, toolbarStates map[string]map[string]any) (Comment, bool) {
	m, ok := raw.(map[string]any)
	if !ok {
		return Comment{}, false
	}
	props, _ := mapPath[map[string]any](m, "properties")
	author, _ := mapPath[map[string]any](m, "author")
	toolbar, _ := mapPath[map[string]any](m, "toolbar")
	if props == nil || author == nil || toolbar == nil {
		return Comment{}, false
	}

	cid, _ := props["commentId"].(string)
	if cid == "" {
		return Comment{}, false
	}

	contentText, _ := mapPath[string](props, "content", "content")
	publishedTime, _ := props["publishedTime"].(string)

	votes, _ := toolbar["likeCountNotliked"].(string)
	votes = strings.TrimSpace(votes)
	if votes == "" {
		votes = "0"
	}

	heart := false
	if state, ok := toolbarStates[asString(props["toolbarStateKey"])]; ok {
		if hs, _ := state["heartState"].(string); hs == "TOOLBAR_HEART_STATE_HEARTED" {
			heart = true
		}
	}

	c := Comment{
		Cid:     cid,
		Text:    contentText,
		Time:    publishedTime,
		Author:  asString(author["displayName"]),
		Channel: asString(author["channelId"]),
		Votes:   votes,
		Replies: asString(toolbar["replyCount"]),
		Photo:   asString(author["avatarThumbnailUrl"]),
		Heart:   heart,
		Reply:   strings.Contains(cid, "."),
	}

	if t, err := ParseRelativeTime(publishedTime, time.Now()); err == nil {
		ts := float64(t.UnixNano()) / 1e9
		c.TimeParsed = &ts
	}
	if paid, ok := payments[cid]; ok {
		c.Paid = paid
	}
	return c, true
}

// mapPath walks a chain of keys through nested map[string]any values and
// asserts the final value to T. Returns the zero value and false if any hop
// fails or the final type doesn't match.
func mapPath[T any](root any, keys ...string) (T, bool) {
	var zero T
	current := root
	for _, k := range keys {
		m, ok := current.(map[string]any)
		if !ok {
			return zero, false
		}
		v, ok := m[k]
		if !ok {
			return zero, false
		}
		current = v
	}
	out, ok := current.(T)
	return out, ok
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
