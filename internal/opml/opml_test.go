package opml

import (
	"bytes"
	"strings"
	"testing"
)

const sample = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0"><head><title>FreshRSS</title></head><body>
<outline text="Blogs">
  <outline text="Fedora Magazine" type="rss" xmlUrl="https://fedoramagazine.org/feed/" htmlUrl="https://fedoramagazine.org/"/>
  <outline text="Nested">
    <outline text="Deep" type="rss" xmlUrl="https://deep.example/rss"/>
  </outline>
</outline>
<outline text="xkcd" title="xkcd.com" type="rss" xmlUrl="https://xkcd.com/atom.xml" htmlUrl="https://xkcd.com/"/>
</body></opml>`

func TestParse(t *testing.T) {
	out, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("len = %d", len(out))
	}
	if out[0].Title != "Fedora Magazine" || out[0].Category != "Blogs" || out[0].XMLURL != "https://fedoramagazine.org/feed/" {
		t.Fatalf("out0 = %+v", out[0])
	}
	if out[1].Category != "Blogs" {
		t.Fatalf("nested folder should flatten to top folder: %+v", out[1])
	}
	if out[2].Title != "xkcd.com" || out[2].Category != "" {
		t.Fatalf("out2 = %+v", out[2])
	}
}

func TestRoundTrip(t *testing.T) {
	b, err := Render([]string{"Blogs"}, map[string][]Feed{
		"":      {{Title: "xkcd", XMLURL: "https://xkcd.com/atom.xml", HTMLURL: "https://xkcd.com/"}},
		"Blogs": {{Title: "Fedora", XMLURL: "https://fedoramagazine.org/feed/"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Parse(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Title != "xkcd" || out[1].Category != "Blogs" || out[1].XMLURL != "https://fedoramagazine.org/feed/" {
		t.Fatalf("round trip: %+v", out)
	}
	if !strings.HasPrefix(string(b), "<?xml") {
		t.Fatalf("missing header")
	}
}
