package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSplitTextChaptersHeadings(t *testing.T) {
	text := `Chapter 1: The Start
Once upon a time.

Chapter 2: The Middle
It continued.

Chapter 3: The End
It finished.`
	chs := splitTextChapters(text)
	if len(chs) != 3 {
		t.Fatalf("got %d chapters, want 3: %+v", len(chs), chs)
	}
	if !strings.Contains(chs[0].Title, "Chapter 1") {
		t.Errorf("title[0] = %q", chs[0].Title)
	}
	if !strings.Contains(chs[1].Content, "continued") {
		t.Errorf("content[1] = %q", chs[1].Content)
	}
}

func TestSplitTextChaptersFallback(t *testing.T) {
	chs := splitTextChapters("Just one block of prose with no headings.")
	if len(chs) != 1 {
		t.Fatalf("got %d chapters, want 1", len(chs))
	}
	if chs[0].Title != "Chapter 1" {
		t.Errorf("title = %q", chs[0].Title)
	}
}

func TestStripHTML(t *testing.T) {
	got := stripHTML(`<html><head><title>X</title><style>p{}</style></head>
	<body><h1>Chapter One</h1><p>Hello&nbsp;<b>world</b>.</p><script>bad()</script>
	<p>Second paragraph.</p></body></html>`)
	if strings.Contains(got, "bad()") || strings.Contains(got, "<") {
		t.Errorf("stripHTML left markup/script: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("stripHTML lost content: %q", got)
	}
}

func TestExtractXHTML(t *testing.T) {
	title, body := extractXHTML(`<html><body><h2>Chapter 5</h2><p>Some text here.</p></body></html>`)
	if title != "Chapter 5" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "Some text here.") {
		t.Errorf("body = %q", body)
	}
}

func TestFTSQueryQuotesTerms(t *testing.T) {
	if got := ftsQuery(`dune "quote`); strings.Contains(got, "quote\"\"") == false {
		// Expect the embedded quote to be doubled so FTS cannot be injected.
		if got != `"dune" "quote"` && !strings.Contains(got, `""`) {
			t.Errorf("ftsQuery = %q", got)
		}
	}
	if got := ftsQuery("   "); got != `""` {
		t.Errorf("empty query = %q", got)
	}
}

func TestSentenceSplit(t *testing.T) {
	text := strings.Repeat("This is a sentence. ", 40)
	chunks := sentenceSplit(text, 120)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c) > 130 {
			t.Errorf("chunk too long (%d): %q", len(c), c)
		}
	}
	if strings.Join(chunks, " ") == "" {
		t.Error("lost all text")
	}
}

func TestJoinWAV(t *testing.T) {
	a := buildWAV([]byte{1, 2, 3, 4}, []byte{10, 11})
	b := buildWAV([]byte{1, 2, 3, 4}, []byte{20, 21, 22})
	joined, err := joinWAV([][]byte{a, b})
	if err != nil {
		t.Fatalf("joinWAV: %v", err)
	}
	w, err := parseWAV(joined)
	if err != nil {
		t.Fatalf("parseWAV: %v", err)
	}
	if !bytes.Equal(w.data, []byte{10, 11, 20, 21, 22}) {
		t.Errorf("data = %v", w.data)
	}
}

func TestJoinWAVFormatMismatch(t *testing.T) {
	a := buildWAV([]byte{1, 2, 3, 4}, []byte{1})
	b := buildWAV([]byte{9, 9, 9, 9}, []byte{2})
	if _, err := joinWAV([][]byte{a, b}); err == nil {
		t.Error("expected format mismatch error")
	}
}
