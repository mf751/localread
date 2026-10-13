package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

// parseNovelFile dispatches on filename extension and returns chapter inputs
// plus a best-effort title/author.
func parseNovelFile(name string, data []byte) (title, author string, chapters []ChapterInput, err error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".txt", ".text", ".md":
		chapters = splitTextChapters(string(data))
		title, author = guessTitleAuthor(chapters)
		if title == "" {
			title = strings.TrimSuffix(path.Base(name), path.Ext(name))
		}
		return title, author, chapters, nil
	case ".epub":
		return parseEPUB(data)
	default:
		return "", "", nil, fmt.Errorf("unsupported file type %q (use .txt or .epub)", path.Ext(name))
	}
}

var chapterHeadRe = regexp.MustCompile(`(?im)^[ \t]*(chapter|chap\.?|part|book|volume|prologue|epilogue|interlude)\b[^\n]{0,80}$`)
var numberedHeadRe = regexp.MustCompile(`(?m)^[ \t]*(\d{1,4})[.)]?[ \t]+[A-Z][^\n]{0,80}$`)

// splitTextChapters breaks plain text into chapters on common heading patterns.
// If no headings are found the whole text becomes one chapter.
func splitTextChapters(text string) []ChapterInput {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	idxs := chapterHeadRe.FindAllStringIndex(text, -1)
	if len(idxs) < 2 {
		if n := numberedHeadRe.FindAllStringIndex(text, -1); len(n) >= 3 {
			idxs = n
		}
	}
	if len(idxs) < 2 {
		body := strings.TrimSpace(text)
		if body == "" {
			return nil
		}
		return []ChapterInput{{Title: "Chapter 1", Content: body}}
	}

	var chapters []ChapterInput
	for i, loc := range idxs {
		start := loc[0]
		end := len(text)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := strings.TrimSpace(text[start:end])
		if block == "" {
			continue
		}
		lines := strings.SplitN(block, "\n", 2)
		heading := strings.TrimSpace(lines[0])
		body := ""
		if len(lines) > 1 {
			body = strings.TrimSpace(lines[1])
		}
		chapters = append(chapters, ChapterInput{Title: heading, Content: body})
	}
	return chapters
}

func guessTitleAuthor(chapters []ChapterInput) (string, string) {
	if len(chapters) == 0 {
		return "", ""
	}
	// "By <author>" line at the top of the first chapter.
	if m := regexp.MustCompile(`(?im)^\s*by[ \t]+(.+)$`).FindStringSubmatch(chapters[0].Content); m != nil {
		return "", strings.TrimSpace(m[1])
	}
	return "", ""
}

// ---------- EPUB ----------

type opfPackage struct {
	Metadata struct {
		Title   []string `xml:"title"`
		Creator []string `xml:"creator"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Refs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func parseEPUB(data []byte) (string, string, []ChapterInput, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", nil, fmt.Errorf("not a valid epub: %w", err)
	}

	root := findOPF(zr)
	if root == "" {
		return "", "", nil, errors.New("epub is missing its package document (container.xml)")
	}
	opfDir := path.Dir(root)

	opfBytes, err := readZipFile(zr, root)
	if err != nil {
		return "", "", nil, err
	}
	var pkg opfPackage
	if err := xml.Unmarshal(opfBytes, &pkg); err != nil {
		return "", "", nil, fmt.Errorf("could not parse epub package: %w", err)
	}

	byID := map[string]string{}
	for _, it := range pkg.Manifest.Items {
		if strings.Contains(it.MediaType, "xhtml") || strings.Contains(it.MediaType, "html") {
			byID[it.ID] = path.Join(opfDir, it.Href)
		}
	}

	var chapters []ChapterInput
	for _, ref := range pkg.Spine.Refs {
		href, ok := byID[ref.IDRef]
		if !ok {
			continue
		}
		raw, err := readZipFile(zr, href)
		if err != nil {
			continue
		}
		title, body := extractXHTML(string(raw))
		if strings.TrimSpace(body) == "" {
			continue
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", len(chapters)+1)
		}
		chapters = append(chapters, ChapterInput{Title: title, Content: body})
	}
	if len(chapters) == 0 {
		return "", "", nil, errors.New("epub contains no readable chapters")
	}

	title := ""
	if len(pkg.Metadata.Title) > 0 {
		title = strings.TrimSpace(pkg.Metadata.Title[0])
	}
	author := ""
	if len(pkg.Metadata.Creator) > 0 {
		author = strings.TrimSpace(pkg.Metadata.Creator[0])
	}
	return title, author, chapters, nil
}

func findOPF(zr *zip.Reader) string {
	raw, err := readZipFile(zr, "META-INF/container.xml")
	if err != nil {
		return ""
	}
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(raw, &c); err != nil || len(c.Rootfiles) == 0 {
		return ""
	}
	return c.Rootfiles[0].FullPath
}

// readZipFile reads a single entry, refusing entries whose path escapes the
// archive root (zip-slip).
func readZipFile(zr *zip.Reader, name string) ([]byte, error) {
	clean := path.Clean("/" + name)[1:]
	for _, f := range zr.File {
		if path.Clean(f.Name) != clean {
			continue
		}
		if f.UncompressedSize64 > 32<<20 {
			return nil, errors.New("epub entry too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 32<<20))
	}
	return nil, fmt.Errorf("epub entry %q not found", name)
}

var (
	hRe    = regexp.MustCompile(`(?is)<h[1-3][^>]*>(.*?)</h[1-3]>`)
	tiRe   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	bodyRe = regexp.MustCompile(`(?is)<body[^>]*>(.*?)</body>`)
)

// extractXHTML pulls a chapter title and plain-text body from an XHTML page.
func extractXHTML(s string) (string, string) {
	title := ""
	if m := tiRe.FindStringSubmatch(s); m != nil {
		title = strings.TrimSpace(stripHTML(m[1]))
	}
	if m := hRe.FindStringSubmatch(s); m != nil {
		if h := strings.TrimSpace(stripHTML(m[1])); h != "" {
			title = h
		}
	}
	body := s
	if m := bodyRe.FindStringSubmatch(s); m != nil {
		body = m[1]
	}
	if title != "" {
		body = strings.Replace(body, title, "", 1)
	}
	return title, stripHTML(body)
}
