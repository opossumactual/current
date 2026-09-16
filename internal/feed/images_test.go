package feed

import (
	"current/internal/store"
	"strings"
	"testing"
)

func TestArticleImages(t *testing.T) {
	a := &store.Article{URL: "https://example.com/story", ImageURL: "/lead.jpg", Fulltext: `<figure><img src="/lead.jpg"><figcaption>The <b>lead</b> caption.</figcaption></figure><img src="next.png" alt="Second image"><img src="javascript:alert(1)"><img src="file:///secret"><img src="data:image/png;base64,abcd">`, Content: `<img src="/lead.jpg"><img src="https://other.example/chart.png" alt="A chart"><img src="https://user:pass@example.com/private.png">`}
	images := ArticleImages(a)
	if len(images) != 3 {
		t.Fatalf("unexpected images: %+v", images)
	}
	if images[0].URL != "https://example.com/lead.jpg" || images[0].Caption != "The lead caption." || images[1].URL != "https://example.com/next.png" || images[1].Caption != "Second image" {
		t.Fatalf("image order/captions: %+v", images)
	}
}

func TestArticleImageSources(t *testing.T) {
	for _, tt := range []struct {
		name, markup, want string
	}{
		{"width", `<img src="/small.jpg" srcset="/medium.jpg 800w, /large.jpg 1800w, /small.jpg 400w">`, "large.jpg"},
		{"density", `<img src="/small.jpg" srcset="/small.jpg 1x, /retina.jpg 2x, /middle.jpg 1.5x">`, "retina.jpg"},
		{"density fallback", `<img src="/small.jpg" srcset="/tiny.jpg 0.5x">`, "small.jpg"},
		{"CDN commas", `<img src="/small.jpg" srcset="/resize/w_400,q_80/photo.jpg 400w, /resize/w_1800,q_90/photo.jpg 1800w">`, "resize/w_1800,q_90/photo.jpg"},
		{"picture", `<picture><source type="image/avif" srcset="/unsupported.avif 2000w"><source media="(max-width: 600px)" srcset="/mobile-crop.jpg 3000w"><source type="image/webp" srcset="/large.webp 1800w"><img src="/small.jpg"></picture>`, "large.webp"},
		{"linked original", `<a href="/original.png?download=1"><img src="/small.jpg" srcset="/medium.jpg 800w"></a>`, "original.png?download=1"},
		{"article link", `<a href="/other-story"><img src="/small.jpg" srcset="/large.jpg 1800w"></a>`, "large.jpg"},
		{"lazy", `<img src="data:image/gif;base64,aaaa" data-src="/small.jpg" data-srcset="/small.jpg 400w, /large.jpg 1800w">`, "large.jpg"},
		{"invalid descriptors", `<img src="/small.jpg" srcset="/negative.jpg -1w, /zero.jpg 0x, /inf.jpg Infx, /nan.jpg NaNx, /mixed.jpg 100w 2x, /unknown.jpg huh, /large.jpg 1600w">`, "large.jpg"},
		{"unsafe URLs", `<img src="/small.jpg" srcset="javascript:alert(1) 9000w, data:image/png;base64,aaaa 8000w, file:///secret 7000w, https://user:pass@example.com/secret 6000w, /large.jpg 1600w">`, "large.jpg"},
		{"unsupported format", `<img src="/small.jpg" srcset="/unsupported.avif 4000w, /large.jpg 1600w">`, "large.jpg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, content := range []string{tt.markup, Sanitize(tt.markup, "https://example.com/story")} {
				a := &store.Article{URL: "https://example.com/story", Content: content}
				images := ArticleImages(a)
				if len(images) != 1 || images[0].URL != "https://example.com/"+tt.want {
					t.Fatalf("sources: %+v from %s", images, content)
				}
			}
		})
	}
}

func TestArticleImagesUpgradeExistingLeadWithoutDuplicates(t *testing.T) {
	a := &store.Article{
		URL: "https://example.com/story", ImageURL: "/small.jpg",
		Fulltext: `<figure><img src="/small.jpg" srcset="/small.jpg 400w, /large.jpg 1800w"><figcaption>Full caption</figcaption></figure><img src="/other.jpg">`,
		Content:  `<img src="/small.jpg" alt="Feed caption"><img src="/large.jpg"><img src="/other.jpg">`,
	}
	images := ArticleImages(a)
	if len(images) != 2 || images[0].URL != "https://example.com/large.jpg" || images[0].Caption != "Full caption" {
		t.Fatalf("lead upgrade/order/deduplication: %+v", images)
	}
	a.Fulltext = `<img src="/small.jpg" alt="Full caption">`
	a.Content = `<img src="/small.jpg" srcset="/small.jpg 400w, /large.jpg 1800w">`
	for _, lead := range []string{"/small.jpg", "/large.jpg"} {
		a.ImageURL = lead
		images = ArticleImages(a)
		if len(images) != 1 || images[0].URL != "https://example.com/large.jpg" || images[0].Caption != "Full caption" {
			t.Fatalf("feed variants must upgrade and merge bare full-text images: %+v", images)
		}
	}
}

func TestResponsiveSourcesSurviveFeedParsing(t *testing.T) {
	body := []byte(`<rss version="2.0"><channel><title>Images</title><link>https://example.com/</link><item><title>Image story</title><link>https://example.com/posts/story</link><description><![CDATA[<picture><source type="image/webp" data-srcset="../large.webp 1800w, ../small.webp 400w"><img src="data:image/gif;base64,abcd" data-src="../small.jpg"></picture>]]></description></item></channel></rss>`)
	parsed, err := Parse(body, "https://example.com/rss")
	if err != nil {
		t.Fatal(err)
	}
	a := parsed.Items[0]
	if a.ImageURL != "https://example.com/large.webp" || !strings.Contains(a.Content, "https://example.com/small.jpg") || !strings.Contains(a.Content, "https://example.com/large.webp 1800w") {
		t.Fatalf("responsive image lost during ingestion: %+v", a)
	}
}
