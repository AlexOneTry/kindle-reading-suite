package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"

	"golang.org/x/net/html"
)

func paragraphs(data []byte) ([]string, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string]*zip.File{}
	for _, f := range r.File {
		files[f.Name] = f
	}
	read := func(name string, limit int64) ([]byte, error) {
		f := files[name]
		if f == nil {
			return nil, fmt.Errorf("в EPUB отсутствует %s", path.Base(name))
		}
		if f.UncompressedSize64 > uint64(limit) {
			return nil, errors.New("раздел EPUB слишком большой")
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(io.LimitReader(r, limit+1))
	}
	var c struct {
		Roots []struct {
			Name string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	raw, err := read("META-INF/container.xml", 128<<10)
	if err != nil {
		return nil, err
	}
	if xml.Unmarshal(raw, &c) != nil || len(c.Roots) == 0 {
		return nil, errors.New("не найден манифест EPUB")
	}
	root := c.Roots[0].Name
	var pkg struct {
		Items []struct {
			ID    string `xml:"id,attr"`
			Href  string `xml:"href,attr"`
			Media string `xml:"media-type,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			ID     string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"spine>itemref"`
	}
	raw, err = read(root, 2<<20)
	if err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(raw, &pkg); err != nil {
		return nil, err
	}
	hrefs := map[string]string{}
	for _, item := range pkg.Items {
		if item.Media != "application/xhtml+xml" && item.Media != "text/html" {
			continue
		}
		u, err := url.Parse(item.Href)
		if err == nil && !u.IsAbs() && u.Host == "" {
			hrefs[item.ID] = path.Join(path.Dir(root), u.Path)
		}
	}
	var out []string
	const maxText = 18 << 20
	remaining := int64(maxText)
	for _, item := range pkg.Spine {
		name := hrefs[item.ID]
		if item.Linear == "no" || name == "" {
			continue
		}
		raw, err := read(name, min(remaining, 6<<20))
		if err != nil {
			return nil, err
		}
		remaining -= int64(len(raw))
		if remaining < 0 {
			return nil, errors.New("текст EPUB превышает лимит")
		}
		out = append(out, chapterText(raw)...)
	}
	if len(out) == 0 {
		return nil, errors.New("в EPUB нет читаемого текста")
	}
	return out, nil
}

func chapterText(raw []byte) []string {
	z := html.NewTokenizer(bytes.NewReader(raw))
	var out []string
	var buf strings.Builder
	inBody := false
	skip := ""
	flush := func() {
		if s := strings.Join(strings.Fields(buf.String()), " "); s != "" {
			out = append(out, s)
		}
		buf.Reset()
	}
	for {
		switch z.Next() {
		case html.ErrorToken:
			flush()
			return out
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			kind := z.Token()
			start := kind.Type != html.EndTagToken
			tag := kind.Data
			if tag == "body" && start {
				inBody = true
			}
			if !inBody {
				continue
			}
			if skip != "" {
				if !start && tag == skip {
					skip = ""
				}
				continue
			}
			switch tag {
			case "script", "style", "svg":
				if kind.Type == html.StartTagToken {
					skip = tag
				}
			case "p", "div", "br", "h1", "h2", "h3", "h4", "li", "blockquote", "section", "hr", "body", "tr":
				flush()
				if tag == "body" && !start {
					return out
				}
			}
		case html.TextToken:
			if inBody && skip == "" {
				buf.Write(z.Text())
			}
		}
	}
}
