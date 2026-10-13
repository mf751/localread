package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CatalogEntry is one installable novel from a source.
type CatalogEntry struct {
	Source      string `json:"source"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Summary     string `json:"summary"`
	DownloadURL string `json:"downloadUrl"`
	Format      string `json:"format"`
}

type opdsFeed struct {
	Entries []struct {
		Title   string `xml:"title"`
		Summary string `xml:"summary"`
		Content string `xml:"content"`
		Author  struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []struct {
			Rel   string `xml:"rel,attr"`
			Href  string `xml:"href,attr"`
			Type  string `xml:"type,attr"`
			Title string `xml:"title,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func (a *App) opdsSources() []string {
	var out []string
	for _, s := range strings.Split(a.cfg.OPDSUrls, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// handleCatalogSearch searches configured sources (OPDS) and the local library.
func (a *App) handleCatalogSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	out := []CatalogEntry{}

	if q != "" {
		local, err := a.store.ListNovels(q)
		if err == nil {
			for _, n := range local {
				out = append(out, CatalogEntry{
					Source: "library", Title: n.Title, Author: n.Author,
					Summary: n.Description, Format: "installed", DownloadURL: "",
				})
			}
		}
	}

	var errorsSeen []string
	for _, src := range a.opdsSources() {
		entries, err := fetchOPDS(src, q)
		if err != nil {
			errorsSeen = append(errorsSeen, fmt.Sprintf("%s: %v", src, err))
			continue
		}
		out = append(out, entries...)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries": out,
		"sources": a.opdsSources(),
		"errors":  errorsSeen,
	})
}

func fetchOPDS(base, q string) ([]CatalogEntry, error) {
	u := base
	if q != "" {
		sep := "?"
		if strings.Contains(base, "?") {
			sep = "&"
		}
		u = base + sep + "q=" + url.QueryEscape(q)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var feed opdsFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("not an OPDS feed: %w", err)
	}
	baseURL, _ := url.Parse(base)

	var out []CatalogEntry
	for _, e := range feed.Entries {
		entry := CatalogEntry{
			Source:  base,
			Title:   strings.TrimSpace(e.Title),
			Author:  strings.TrimSpace(e.Author.Name),
			Summary: strings.TrimSpace(firstNonEmpty(e.Summary, e.Content)),
		}
		for _, l := range e.Links {
			if !strings.Contains(l.Rel, "acquisition") {
				continue
			}
			href := l.Href
			if baseURL != nil {
				if ref, err := url.Parse(l.Href); err == nil {
					href = baseURL.ResolveReference(ref).String()
				}
			}
			entry.DownloadURL = href
			switch {
			case strings.Contains(l.Type, "epub"):
				entry.Format = "epub"
			case strings.Contains(l.Type, "text/plain"):
				entry.Format = "txt"
			default:
				entry.Format = l.Type
			}
			if entry.Format == "epub" || entry.Format == "txt" {
				break
			}
		}
		if entry.DownloadURL == "" || entry.Title == "" {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

// handleCatalogInstall downloads a catalog entry and imports it.
func (a *App) handleCatalogInstall(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	var in struct {
		URL    string `json:"url"`
		Title  string `json:"title"`
		Author string `json:"author"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	parsed, err := url.Parse(in.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeErr(w, http.StatusBadRequest, "download url must be http(s)")
		return
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(in.URL)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not download: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("download returned http %d", resp.StatusCode))
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, a.cfg.MaxUploadBytes))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "could not read download")
		return
	}

	ext := ".epub"
	if strings.Contains(resp.Header.Get("Content-Type"), "text/plain") || strings.HasSuffix(parsed.Path, ".txt") {
		ext = ".txt"
	}
	title, author, chapters, err := parseNovelFile("download"+ext, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Title != "" {
		title = in.Title
	}
	if in.Author != "" {
		author = in.Author
	}
	chapters = dropEmpty(chapters)
	if len(chapters) == 0 {
		writeErr(w, http.StatusBadRequest, "no readable chapters found")
		return
	}
	if title == "" {
		title = "Untitled"
	}

	n, err := a.store.CreateNovel(NovelInput{
		Title: title, Author: author, Description: firstParagraph(chapters),
		Source: "opds", SourceURL: in.URL, CreatedBy: u.ID, Chapters: chapters,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save novel")
		return
	}
	_ = a.store.AddHistory(u.ID, n.ID, "", "import", "installed: "+title)
	writeJSON(w, http.StatusCreated, n)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
