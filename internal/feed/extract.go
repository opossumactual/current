package feed

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	readability "github.com/go-shiori/go-readability"
)

// MinFulltextLen is the content length below which extraction is attempted.
const MinFulltextLen = 1500

// Extract downloads articleURL and returns the sanitized main content.
func Extract(ctx context.Context, client *http.Client, articleURL string) (string, error) {
	res, err := Fetch(ctx, client, articleURL, "", "")
	if err != nil {
		return "", err
	}
	u, err := url.Parse(res.FinalURL)
	if err != nil {
		return "", err
	}
	art, err := readability.FromReader(strings.NewReader(string(res.Body)), u)
	if err != nil {
		return "", err
	}
	out := Sanitize(art.Content, res.FinalURL)
	if strings.TrimSpace(out) == "" {
		return "", errors.New("no content extracted")
	}
	return out, nil
}
