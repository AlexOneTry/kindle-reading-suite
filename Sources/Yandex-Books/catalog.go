package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Search operation accepted by Bookmate's gateway. See THIRD-PARTY.txt.
//
//go:embed catalog-query.graphql
var catalogQuery string

const catalogEndpoint = "https://api-gateway.bookmate.yandex.net/graphql"

func percentFromAPI(progress float64) float64 {
	if progress <= 1 {
		progress *= 100
	}
	return min(100, max(0, progress))
}

func (a *api) searchCatalog(query, cursor string) ([]book, string, error) {
	payload, err := json.Marshal(map[string]any{
		"operationName": "Search", "query": catalogQuery,
		"variables": map[string]any{"query": map[string]any{
			"cursor": cursor, "noMisspell": false, "query": query, "types": []string{},
		}},
	})
	if err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, catalogEndpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, "", err
	}
	a.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "multipart/mixed; deferSpec=20220824, application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("каталог недоступен: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, "", errSession
	}
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("каталог вернул HTTP %d", resp.StatusCode)
	}
	const limit = 8 << 20
	var body io.Reader = io.LimitReader(resp.Body, limit+1)
	media, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if media == "multipart/mixed" {
		part, err := multipart.NewReader(body, params["boundary"]).NextPart()
		if err != nil {
			return nil, "", errors.New("не удалось прочитать ответ каталога")
		}
		body = part
	}
	var response struct {
		Data struct {
			Search *struct {
				Cursor string
				Page   []struct {
					Kind     string `json:"__typename"`
					Book     *book
					Progress *struct {
						Progress  *float64
						Finished  bool
						InLibrary bool
					}
				}
			}
		}
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil || response.Data.Search == nil {
		return nil, "", errors.New("Яндекс не вернул результаты поиска")
	}
	var books []book
	seen := map[string]bool{}
	for _, hit := range response.Data.Search.Page {
		if (hit.Kind != "TextBook" && hit.Kind != "TextSerial") || hit.Book == nil || hit.Book.id() == "" || seen[hit.Book.id()] {
			continue
		}
		b := *hit.Book
		if hit.Progress != nil {
			b.OnYandex = hit.Progress.InLibrary
		}
		seen[b.id()] = true
		if hit.Progress != nil && hit.Progress.Progress != nil {
			b.RemoteKnown = true
			b.RemoteProgress = percentFromAPI(*hit.Progress.Progress)
		}
		if hit.Progress != nil && hit.Progress.Finished {
			b.RemoteState = "finished"
		}
		books = append(books, b)
	}
	return books, response.Data.Search.Cursor, nil
}

type catalogView struct {
	Query, Cursor string
	Books         []book
	Start         int
	Available     bool
}

func (a *app) browseCatalog(resume bool) error {
	view := a.shelf.Catalog
	view.Start = max(0, view.Start/catalogRows*catalogRows)
	if !resume || view.Query == "" {
		query, err := a.catalogPrompt()
		if err != nil || strings.TrimSpace(query) == "" {
			return err
		}
		view = catalogView{Query: strings.TrimSpace(query)}
	}
	load := len(view.Books) == 0 && view.Cursor == ""
	advance := false
	for {
		if load {
			if err := a.show(a.ui.message("Ищу в каталоге", view.Query, "")); err != nil {
				return err
			}
			found, next, err := a.client.searchCatalog(view.Query, view.Cursor)
			if err != nil {
				retry, e := a.confirm("Поиск недоступен", err.Error(), "Повторить")
				if e != nil || !retry {
					return e
				}
				continue
			}
			seen := map[string]bool{}
			for _, b := range view.Books {
				seen[b.id()] = true
			}
			for _, b := range found {
				if !seen[b.id()] {
					seen[b.id()] = true
					view.Books = append(view.Books, b)
				}
			}
			if next == view.Cursor {
				next = ""
			}
			view.Cursor = next
			load = false
		}
		var books []book
		for _, b := range view.Books {
			if !view.Available || a.availability(b) == "Доступна" {
				books = append(books, b)
			}
		}
		if advance && view.Start+catalogRows < len(books) {
			view.Start += catalogRows
		}
		advance = false
		if view.Start >= len(books) {
			view.Start = max(0, (len(books)-1)/catalogRows*catalogRows)
		}
		if err := a.show(a.ui.catalog(books, view.Start, view.Query, view.Cursor != "", view.Available)); err != nil {
			return err
		}
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		switch {
		case t.y < 90 && t.x < width/3:
			return nil
		case hit(t, catalogFilter):
			view.Available = !view.Available
			view.Start = 0
			if view.Available {
				a.refreshAccess()
			}
		case hit(t, footerAction) || hit(t, catalogSearch):
			next, err := a.catalogPrompt()
			if err != nil {
				return err
			}
			if strings.TrimSpace(next) != "" && next != view.Query {
				view = catalogView{Query: strings.TrimSpace(next), Available: view.Available}
				load = true
			}
		case hit(t, footerPrevious) && view.Start >= catalogRows:
			view.Start -= catalogRows
		case hit(t, footerNext):
			if view.Start+catalogRows < len(books) {
				view.Start += catalogRows
			} else if view.Cursor != "" {
				load = true
				advance = true
			}
		case hit(t, catalogList):
			row := (t.y - catalogList.Min.Y) / catalogRowHeight
			_, menu := catalogRow(row)
			i := view.Start + row
			if i >= len(books) {
				continue
			}
			a.shelf.Catalog = view
			a.returnCatalog = true
			if err := a.saveShelf(); err != nil {
				return err
			}
			if hit(t, menu) {
				err = a.card(books[i])
			} else {
				err = a.openKOReader(books[i])
			}
			a.returnCatalog = false
			if err != nil {
				return err
			}
		}
	}
}

const catalogRows = 5
const catalogRowHeight = 111

var (
	catalogSearch = image.Rect(44, 187, 460, 260)
	catalogFilter = image.Rect(473, 187, 714, 260)
	catalogList   = image.Rect(44, 324, 714, 324+catalogRows*catalogRowHeight)
)

func catalogRow(index int) (image.Rectangle, image.Rectangle) {
	y := catalogList.Min.Y + index*catalogRowHeight
	return image.Rect(44, y, 714, y+catalogRowHeight), image.Rect(621, y+10, 714, y+catalogRowHeight-10)
}

func (u *ui) catalog(books []book, start int, query string, more, available bool) *image.Gray {
	img := canvas()
	u.top(img, "‹ Полка", "Выход")
	label(img, u.heading, margin, 148, "Каталог", 20)
	u.actionLabel(img, catalogSearch, "Поиск: "+query, u.small, false)
	filter := "Все книги ›"
	if available {
		filter = "Доступные ›"
	}
	u.button(img, catalogFilter, filter, false)
	label(img, u.small, margin, 304, "Результаты Яндекс Книг", 60)
	for i, b := range books[start:min(start+catalogRows, len(books))] {
		area, actions := catalogRow(i)
		u.listRow(img, area, actions, b.title(), b.authors())
	}
	if len(books) == 0 {
		message := "Текстовые книги не найдены"
		if available {
			message = "Нет книг с подтверждённым доступом"
		}
		label(img, u.normal, margin, 391, message, 30)
	}
	left, right := "", ""
	if start > 0 {
		left = "‹ Назад"
	}
	if start+catalogRows < len(books) || more {
		right = "Дальше ›"
	}
	u.actionsFooter(img, left, "Новый поиск", right)
	return img
}
