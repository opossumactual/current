// Package feed fetches, parses and cleans syndication feeds.
package feed

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UserAgent identifies Current to feed servers. It carries a FreshRSS
// compatibility token because several sites (CENTCOM, CAL FIRE, ...) keep an
// allowlist of known readers that includes FreshRSS. Override with the
// CURRENT_USER_AGENT environment variable.
var UserAgent = "Current/0.1 FreshRSS/1.30.0 (Linux; https://freshrss.org)"

// DefaultRetryAfter is the per-host hold applied to a 429 or 503 that carries
// no Retry-After guidance. Matches FreshRSS's retry_after_default.
const DefaultRetryAfter = 25 * time.Minute

// MaxBodyBytes caps a fetched document.
const MaxBodyBytes = 10 << 20

// Result is the outcome of Fetch.
type Result struct {
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
	ContentType  string
	FinalURL     string
	// RetryAfter is how long the server asked us to wait before the next
	// request to this host, from Retry-After or X-RateLimit-* headers. Zero
	// when the server gave no guidance.
	RetryAfter time.Duration
}

// retryAfter reads server rate-limit guidance. A 429 or 503 without headers
// yields DefaultRetryAfter; a successful response with an exhausted
// X-RateLimit budget yields the reset interval.
func retryAfter(h http.Header, status int) time.Duration {
	if ra := strings.TrimSpace(h.Get("Retry-After")); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs > 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(ra); err == nil {
			if d := time.Until(t); d > 0 {
				return d
			}
		}
	}
	reset, _ := strconv.ParseFloat(strings.TrimSpace(h.Get("X-RateLimit-Reset")), 64)
	remaining, hasRemaining := parseFloatHeader(h, "X-RateLimit-Remaining")
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		if reset > 0 {
			return time.Duration(reset * float64(time.Second))
		}
		return DefaultRetryAfter
	}
	if hasRemaining && remaining < 1 && reset > 0 {
		return time.Duration(reset * float64(time.Second))
	}
	return 0
}

func parseFloatHeader(h http.Header, name string) (float64, bool) {
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(v, 64)
	return f, err == nil
}

// NewClient returns an HTTP client suitable for crawling.
func NewClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// Fetch performs a conditional GET.
func Fetch(ctx context.Context, client *http.Client, url, etag, lastModified string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, text/html;q=0.8, */*;q=0.5")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	res := &Result{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), ContentType: resp.Header.Get("Content-Type"), FinalURL: resp.Request.URL.String()}
	res.RetryAfter = retryAfter(resp.Header, resp.StatusCode)
	if resp.StatusCode == http.StatusNotModified {
		res.NotModified = true
		if res.ETag == "" {
			res.ETag = etag
		}
		if res.LastModified == "" {
			res.LastModified = lastModified
		}
		return res, nil
	}
	if resp.StatusCode == http.StatusForbidden && CurlPath != "" {
		// Bot-management firewalls that fingerprint TLS reject Go but accept
		// curl/OpenSSL (what FreshRSS uses). Retry once through curl.
		if cres, cerr := curlFetch(ctx, url, etag, lastModified); cerr == nil {
			return cres, nil
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The partial result carries RetryAfter so callers can back off the host.
		return res, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes))
	if err != nil {
		return nil, err
	}
	res.Body = body
	return res, nil
}
