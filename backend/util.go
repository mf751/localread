package main

import (
	"crypto/rand"
	"encoding/hex"
	"html"
	"regexp"
	"strings"
	"unicode"
)

const idAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

// randomID returns a URL-safe identifier of n characters. It only ever emits
// characters accepted by safeID, so ids can be used to build cache paths.
func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand should not fail; fall back to a fixed but unique-ish value.
		for i := range b {
			b[i] = byte(i)
		}
	}
	for i := range b {
		b[i] = idAlphabet[int(b[i])%len(idAlphabet)]
	}
	return string(b)
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(randomID(32)))
	}
	return hex.EncodeToString(b)
}

var wordRe = regexp.MustCompile(`\S+`)

func wordCount(s string) int { return len(wordRe.FindAllString(s, -1)) }

// ftsQuery turns a user search string into a safe FTS5 MATCH expression by
// quoting each term, so search input can never inject FTS operators.
func ftsQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return `""`
	}
	for i, f := range fields {
		fields[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(fields, " ")
}

var (
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	scriptRe = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	wsRe     = regexp.MustCompile(`[ \t]+`)
	nlRe     = regexp.MustCompile(`\n{3,}`)
)

// stripHTML converts an XHTML fragment into plain text paragraphs.
func stripHTML(s string) string {
	s = scriptRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = regexp.MustCompile(`(?i)</p>|</h[1-6]>|</div>`).ReplaceAllString(s, "\n\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = wsRe.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	s = nlRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

// slugify makes a filesystem-safe name fragment.
func slugify(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) && r < 128:
			b.WriteRune(r)
		case unicode.IsDigit(r) && r < 128:
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	if out == "" {
		out = "novel"
	}
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// sentenceSplit splits text into TTS-friendly chunks no longer than maxChars,
// preferring sentence, then comma/space boundaries.
func sentenceSplit(text string, maxChars int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxChars < 80 {
		maxChars = 80
	}
	var chunks []string
	remaining := text
	for len(remaining) > maxChars {
		cut := -1
		// Prefer a sentence end within the window.
		for _, sep := range []string{". ", "! ", "? ", ".\n", "… ", "\n\n"} {
			if i := strings.LastIndex(remaining[:maxChars], sep); i > cut {
				cut = i + len(sep)
			}
		}
		if cut <= 0 {
			if i := strings.LastIndexAny(remaining[:maxChars], ",;: "); i > 0 {
				cut = i + 1
			}
		}
		if cut <= 0 {
			cut = maxChars
		}
		chunks = append(chunks, strings.TrimSpace(remaining[:cut]))
		remaining = strings.TrimSpace(remaining[cut:])
	}
	if remaining != "" {
		chunks = append(chunks, remaining)
	}
	return chunks
}
