package feed

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// CurlPath is the curl binary used as a fallback fetcher, or "" when curl is
// not installed. Some bot-management firewalls (Akamai on centcom.mil and
// fire.ca.gov, for example) reject Go's TLS fingerprint while accepting
// curl/OpenSSL, which is what FreshRSS uses via PHP. When the Go client gets
// a 403, Fetch retries once through curl.
var CurlPath, _ = exec.LookPath("curl")

// curlFetch is swapped in tests.
var curlFetch = fetchCurl

// fetchCurl performs the same conditional GET as Fetch through the curl binary.
func fetchCurl(ctx context.Context, url, etag, lastModified string) (*Result, error) {
	if CurlPath == "" {
		return nil, errors.New("curl not available")
	}
	dir, err := os.MkdirTemp("", "current-curl-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	headerFile := filepath.Join(dir, "headers")
	bodyFile := filepath.Join(dir, "body")
	args := []string{
		"--silent", "--show-error", "--location", "--compressed",
		"--max-time", "30", "--max-filesize", strconv.Itoa(MaxBodyBytes),
		"--user-agent", UserAgent,
		"--header", "Accept: application/rss+xml, application/atom+xml, application/feed+json, application/xml;q=0.9, text/html;q=0.8, */*;q=0.5",
		"--dump-header", headerFile, "--output", bodyFile,
		"--write-out", "%{http_code}\n%{url_effective}",
	}
	if etag != "" {
		args = append(args, "--header", "If-None-Match: "+etag)
	}
	if lastModified != "" {
		args = append(args, "--header", "If-Modified-Since: "+lastModified)
	}
	args = append(args, url)
	cmd := exec.CommandContext(ctx, CurlPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("curl: %s", msg)
	}
	out := strings.SplitN(strings.TrimSpace(stdout.String()), "\n", 2)
	status, _ := strconv.Atoi(strings.TrimSpace(out[0]))
	res := &Result{FinalURL: url}
	if len(out) == 2 {
		res.FinalURL = strings.TrimSpace(out[1])
	}
	hdr := lastHeaderBlock(headerFile)
	res.ETag = hdr.Get("ETag")
	res.LastModified = hdr.Get("Last-Modified")
	res.ContentType = hdr.Get("Content-Type")
	res.RetryAfter = retryAfter(hdr, status)
	if status == http.StatusNotModified {
		res.NotModified = true
		if res.ETag == "" {
			res.ETag = etag
		}
		if res.LastModified == "" {
			res.LastModified = lastModified
		}
		return res, nil
	}
	if status < 200 || status >= 300 {
		return res, fmt.Errorf("HTTP %d", status)
	}
	body, err := os.ReadFile(bodyFile)
	if err != nil {
		return nil, err
	}
	res.Body = body
	return res, nil
}

// lastHeaderBlock parses the final response's headers from a curl
// --dump-header file, which holds one block per redirect hop.
func lastHeaderBlock(path string) http.Header {
	raw, err := os.ReadFile(path)
	if err != nil {
		return http.Header{}
	}
	blocks := bytes.Split(bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n")), []byte("\n\n"))
	var last []byte
	for _, b := range blocks {
		if bytes.HasPrefix(bytes.TrimSpace(b), []byte("HTTP/")) {
			last = b
		}
	}
	if last == nil {
		return http.Header{}
	}
	r := bufio.NewReader(bytes.NewReader(last))
	r.ReadString('\n') // status line
	mh, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil && len(mh) == 0 {
		return http.Header{}
	}
	return http.Header(mh)
}
