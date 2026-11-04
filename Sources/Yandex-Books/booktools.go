package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"golang.org/x/net/html"
)

type coverImage struct{ URL, Small, Large string }
type seriesInfo struct {
	UUID, Name, Title string
	Number, Position  float64
	Books             []book
}

func (s seriesInfo) title() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Title
}

func (b book) seriesInfo() seriesInfo {
	var s seriesInfo
	if json.Unmarshal(b.Series, &s) != nil {
		var all []seriesInfo
		if json.Unmarshal(b.Series, &all) == nil && len(all) > 0 {
			s = all[0]
		}
	}
	if s.title() == "" {
		s.Name = b.SeriesName
	}
	if s.Number == 0 {
		s.Number = s.Position
	}
	if number, err := strconv.ParseFloat(strings.Trim(string(b.SeriesPosition), `"`), 64); err == nil && number > 0 {
		s.Number = number
	}
	return s
}

// KOReader also gets series information from EPUB metadata. Use it when the API
// omits the series for a downloaded title.
func localSeries(b book) seriesInfo {
	current := b.seriesInfo()
	if current.title() != "" && current.Number > 0 {
		return current
	}
	path, err := bookFile(koReaderBooks, b, false)
	if err != nil {
		return current
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		return current
	}
	defer archive.Close()
	decode := func(name string, target any) error {
		f, err := archive.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		return xml.NewDecoder(io.LimitReader(f, 2<<20)).Decode(target)
	}
	var container struct {
		Root struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if decode("META-INF/container.xml", &container) != nil {
		return current
	}
	var pkg struct {
		Meta []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Refines  string `xml:"refines,attr"`
			ID       string `xml:"id,attr"`
			Text     string `xml:",chardata"`
		} `xml:"metadata>meta"`
	}
	if decode(container.Root.Path, &pkg) != nil {
		return current
	}
	for _, m := range pkg.Meta {
		switch m.Name {
		case "calibre:series":
			if current.title() == "" {
				current.Name = m.Content
			}
		case "calibre:series_index":
			if current.Number == 0 {
				current.Number, _ = strconv.ParseFloat(m.Content, 64)
			}
		}
	}
	if current.title() != "" {
		return current
	}
	for _, m := range pkg.Meta {
		if m.Property != "belongs-to-collection" || m.ID == "" {
			continue
		}
		series := false
		var number float64
		for _, detail := range pkg.Meta {
			if detail.Refines != "#"+m.ID {
				continue
			}
			if detail.Property == "collection-type" && strings.TrimSpace(detail.Text) == "series" {
				series = true
			}
			if detail.Property == "group-position" {
				number, _ = strconv.ParseFloat(strings.TrimSpace(detail.Text), 64)
			}
		}
		if series {
			current.Name, current.Number = strings.TrimSpace(m.Text), number
			break
		}
	}
	return current
}

func (b book) firstAuthor() author {
	for _, raw := range []json.RawMessage{b.Authors, b.AuthorsObjects} {
		var people []author
		if json.Unmarshal(raw, &people) == nil {
			for _, p := range people {
				if p.UUID != "" {
					return p
				}
			}
		}
	}
	return author{Name: strings.Split(b.authors(), ",")[0]}
}

func (a *api) details(b book) (book, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := a.get(ctx, "/books/"+url.PathEscape(b.id()), "application/json", 2<<20)
	if err != nil {
		return b, err
	}
	var result struct{ Book *book }
	if err := json.Unmarshal(data, &result); err != nil || result.Book == nil {
		return b, errors.New("карточка недоступна")
	}
	out := *result.Book
	if out.id() == "" {
		out.UUID = b.id()
	}
	if out.Name == "" && out.Title == "" {
		out.Title = b.title()
	}
	if out.authors() == "" {
		out.Authors = b.Authors
		out.AuthorsObjects = b.AuthorsObjects
	}
	if out.Cover == (coverImage{}) {
		out.Cover = b.Cover
	}
	if out.Annotation == "" {
		out.Annotation = b.Annotation
	}
	if out.EditorAnnotation == "" {
		out.EditorAnnotation = b.EditorAnnotation
	}
	if len(out.Series) == 0 {
		out.Series = b.Series
		out.SeriesName = b.SeriesName
		out.SeriesPosition = b.SeriesPosition
	}
	if len(out.Levels) == 0 {
		out.Levels = b.Levels
	}
	if len(out.LevelsREST) == 0 {
		out.LevelsREST = b.LevelsREST
	}
	if out.Level == "" {
		out.Level = b.Level
	}
	if out.CanRead == nil {
		out.CanRead = b.CanRead
	}
	out.OnYandex = b.OnYandex
	out.RemoteKnown, out.RemoteProgress, out.RemoteState = b.RemoteKnown, b.RemoteProgress, b.RemoteState
	return out, nil
}

func (a *api) authorBooks(p author, page int) ([]book, error) {
	path := fmt.Sprintf("/authors/%s/books?role=author&page=%d&per_page=20", url.PathEscape(p.UUID), page)
	data, err := a.get(context.Background(), path, "application/json", 4<<20)
	if err != nil {
		return nil, err
	}
	var result struct{ Books []book }
	err = json.Unmarshal(data, &result)
	return result.Books, err
}

func (a *api) addToYandex(b book) error {
	body, err := json.Marshal(map[string]string{"book_uuid": b.id()})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, a.base+"/profile/library_cards", bytes.NewReader(body))
	if err != nil {
		return err
	}
	a.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return errSession
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Яндекс вернул HTTP %d", resp.StatusCode)
	}
	return nil
}

func (a *app) refreshAccess() {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	data, err := a.client.get(ctx, "/profile/access_levels", "application/json", 1<<20)
	if err != nil {
		return
	}
	var levels []string
	if json.Unmarshal(data, &levels) != nil {
		var object struct {
			Levels       []string `json:"levels"`
			AccessLevels []string `json:"access_levels"`
		}
		if json.Unmarshal(data, &object) != nil {
			return
		}
		levels = append(object.Levels, object.AccessLevels...)
	}
	if len(levels) > 0 {
		a.shelf.Access = levels
	}
}

func (a *app) availability(b book) string {
	if downloadSize(b) > 0 {
		return "Доступна"
	}
	if b.CanRead != nil {
		if *b.CanRead {
			return "Доступна"
		}
		return "Нет доступа"
	}
	levels := append(append(append([]string(nil), b.Levels...), b.LevelsREST...), b.Level)
	for _, level := range levels {
		if strings.EqualFold(level, "free") {
			return "Доступна"
		}
		for _, active := range a.shelf.Access {
			if level != "" && strings.EqualFold(level, active) {
				return "Доступна"
			}
		}
	}
	return "Доступ не подтверждён"
}

func (a *api) cover(c coverImage) image.Image {
	for _, address := range []string{c.Small, c.URL, c.Large} {
		u, err := url.Parse(address)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		if !(strings.HasSuffix(host, ".yandex.net") || strings.HasSuffix(host, ".yandex.ru") || strings.HasSuffix(host, ".bookmate.com") || strings.HasSuffix(host, ".yandexcloud.net")) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			cancel()
			continue
		}
		resp, err := a.client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		resp.Body.Close()
		cancel()
		if err != nil || resp.StatusCode != 200 || len(data) > 2<<20 {
			continue
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width > 2400 || config.Height > 3200 {
			continue
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err == nil {
			return img
		}
	}
	return nil
}

func annotationCount(b book) int {
	file, err := bookFile(koReaderBooks, b, false)
	if err != nil {
		return 0
	}
	path := filepath.Join(strings.TrimSuffix(file, ".epub")+".sdr", "metadata.epub.lua")
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<20))
	if err != nil {
		return 0
	}
	body := annotationBlock.FindSubmatch(data)
	if body == nil {
		return 0
	}
	return len(annotationDrawer.FindAll(body[1], -1))
}

var annotationBlock = regexp.MustCompile(`(?ms)^    \["annotations"\] = \{\s*\n(.*?)^    \}`)
var annotationDrawer = regexp.MustCompile(`(?m)^\s*\["drawer"\] = "`)

func hasBook(books []book, id string) bool {
	for _, b := range books {
		if b.id() == id {
			return true
		}
	}
	return false
}
func toggleBook(books []book, b book) []book {
	for i, found := range books {
		if found.id() == b.id() {
			return append(books[:i], books[i+1:]...)
		}
	}
	return append(books, b)
}

func (a *app) card(b book) error {
	if b.Annotation == "" || b.Cover == (coverImage{}) {
		if err := a.show(a.ui.message("Карточка книги", b.title(), "")); err != nil {
			return err
		}
		b, _ = a.client.details(b)
	}
	cover := a.client.cover(b.Cover)
	for _, item := range a.shelf.Books {
		if item.id() == b.id() && item.OnYandex {
			b.OnYandex = true
			break
		}
	}
	for {
		count := annotationCount(b)
		labels := []string{"Читать в KOReader", "Добавить в «Мои книги» Яндекса", "В «Хочу прочитать»", "В очередь скачивания", "Другие книги автора", "Книги серии", fmt.Sprintf("Заметки в KOReader · %d", count)}
		if b.OnYandex {
			labels[1] = "Уже в «Моих книгах» Яндекса"
		}
		if hasBook(a.shelf.Wishlist, b.id()) {
			labels[2] = "Убрать из «Хочу прочитать»"
		}
		if hasBook(a.shelf.Queue, b.id()) {
			labels[3] = "Убрать из очереди"
		}
		img := a.ui.bookCard(b, cover, a.availability(b), labels)
		if err := a.show(img); err != nil {
			return err
		}
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		if t.y < 90 && t.x < width/2 {
			return nil
		}
		if hit(t, footerPrevious) {
			if err := a.chooseReader(b); err != nil {
				return err
			}
			continue
		}
		if hit(t, footerAction) {
			description := descriptionText(b)
			if description == "" {
				description = "Яндекс не прислал аннотацию."
			}
			if err := a.showDescription(description); err != nil {
				return err
			}
			continue
		}
		if !hit(t, cardActionArea) {
			continue
		}
		switch (t.y - cardActionArea.Min.Y) / cardActionHeight {
		case 0:
			return a.openKOReader(b)
		case 1:
			if b.OnYandex {
				continue
			}
			yes, err := a.confirm("Добавить в Яндекс?", b.title(), "Добавить")
			if err != nil {
				return err
			}
			if !yes {
				continue
			}
			if err := a.client.addToYandex(b); err != nil {
				if err := a.note("Не добавлено", err.Error(), "Закрыть"); err != nil {
					return err
				}
				continue
			}
			b.OnYandex = true
			a.putShelfBook(b)
			if err := a.saveShelf(); err != nil {
				return err
			}
		case 2:
			a.shelf.Wishlist = toggleBook(a.shelf.Wishlist, b)
			if err := a.saveShelf(); err != nil {
				return err
			}
		case 3:
			a.shelf.Queue = toggleBook(a.shelf.Queue, b)
			if err := a.saveShelf(); err != nil {
				return err
			}
		case 4:
			if err := a.browseAuthor(b.firstAuthor()); err != nil {
				return err
			}
		case 5:
			if err := a.browseSeries(b); err != nil {
				return err
			}
		case 6:
			if _, err := os.Stat("/mnt/us/koreader/plugins/yandexbooksnotes.koplugin/main.lua"); err != nil {
				return a.note("Нужно дополнение KOReader", "Установи папку плагина из архива обновления в koreader/plugins.", "К полке")
			}
			a.openNotes = true
			defer func() { a.openNotes = false }()
			return a.openKOReader(b)
		}
	}
}

func (a *app) showDescription(description string) error {
	pages := a.ui.paginate([]string{description})
	for page := 0; ; {
		if err := a.show(a.ui.reader(book{Title: "Аннотация"}, pages, page)); err != nil {
			return err
		}
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		if t.y < 90 && t.x < width/2 {
			return nil
		}
		if t.y < 135 {
			continue
		}
		if t.x < width/3 && page > 0 {
			page--
		}
		if t.x > width*2/3 && page+1 < len(pages) {
			page++
		}
	}
}

func (a *app) putShelfBook(b book) {
	for i, old := range a.shelf.Books {
		if old.id() == b.id() {
			a.shelf.Books[i] = b
			return
		}
	}
	a.shelf.Books = append(a.shelf.Books, b)
}

func descriptionText(b book) string {
	for _, s := range []string{b.Annotation, b.EditorAnnotation, b.EditorAnnotationREST} {
		if s == "" {
			continue
		}
		z := html.NewTokenizer(strings.NewReader(s))
		var words []string
		for token := z.Next(); token != html.ErrorToken; token = z.Next() {
			if token == html.TextToken {
				words = append(words, strings.Fields(string(z.Text()))...)
			}
		}
		if len(words) > 0 {
			return strings.Join(words, " ")
		}
	}
	return ""
}

const cardActionHeight = 68

var cardActionArea = image.Rect(44, 436, 714, 436+7*cardActionHeight)

func (u *ui) bookCard(b book, cover image.Image, access string, labels []string) *image.Gray {
	img := canvas()
	u.top(img, "‹ Назад", "Выход")
	x := margin
	if cover != nil {
		bounds := cover.Bounds()
		h := 230
		w := min(170, max(1, bounds.Dx()*h/bounds.Dy()))
		draw.ApproxBiLinear.Scale(img, image.Rect(x, 116, x+w, 116+h), cover, bounds, draw.Over, nil)
		x += w + 22
	} else {
		r := image.Rect(x, 116, x+143, 346)
		buttonFrame(img, r, false)
		box(img, x+17, 134, 2, 194, 148)
		box(img, x+31, 190, 83, 3, 100)
		box(img, x+31, 207, 63, 2, 148)
		box(img, x+31, 222, 73, 2, 148)
		x += 165
	}
	for i, line := range wrap(u.bold, b.title(), width-margin-x) {
		if i == 3 {
			break
		}
		label(img, u.bold, x, 150+i*37, line, 15)
	}
	label(img, u.small, x, 273, shorten(u.small, b.authors(), width-margin-x), 60)
	series := localSeries(b)
	if name := series.title(); name != "" {
		if series.Number > 0 {
			name += fmt.Sprintf(" · № %g", series.Number)
		}
		label(img, u.small, x, 308, shorten(u.small, name, width-margin-x), 60)
	}
	label(img, u.small, x, 344, access, 60)
	for i, line := range wrap(u.small, descriptionText(b), width-2*margin) {
		if i >= 2 {
			break
		}
		label(img, u.small, margin, 385+i*28, line, 45)
	}
	for i, text := range labels {
		y := cardActionArea.Min.Y + i*cardActionHeight
		u.actionLabel(img, image.Rect(margin, y, width-margin, y+60), text, u.small, i == 0)
	}
	u.actionsFooter(img, "Файл и чтение", "Аннотация", "")
	return img
}

func (a *app) catalogPrompt() (string, error) {
	items := append([]string{"Новый запрос"}, a.shelf.SearchHistory...)
	index, err := a.pick("Поиск в каталоге", items)
	if err != nil || index < 0 {
		return "", err
	}
	query := ""
	if index == 0 {
		query, err = a.searchKeyboard("", "Поиск в каталоге")
	} else {
		query = items[index]
	}
	if err != nil || strings.TrimSpace(query) == "" {
		return "", err
	}
	query = strings.TrimSpace(query)
	history := []string{query}
	for _, old := range a.shelf.SearchHistory {
		if !strings.EqualFold(old, query) && len(history) < 6 {
			history = append(history, old)
		}
	}
	a.shelf.SearchHistory = history
	return query, a.saveShelf()
}

func (a *app) pick(title string, items []string) (int, error) {
	start := 0
	for {
		img := canvas()
		a.ui.top(img, "‹ Назад", "Выход")
		label(img, a.ui.heading, margin, 157, shorten(a.ui.heading, title, width-2*margin), 12)
		for i, item := range items[start:min(start+7, len(items))] {
			y := 210 + i*97
			box(img, margin, y, width-2*margin, 82, 236)
			label(img, a.ui.normal, margin+14, y+51, shorten(a.ui.normal, item, width-2*margin-28), 24)
		}
		left, right := "", ""
		if start > 0 {
			left = "‹ Назад"
		}
		if start+7 < len(items) {
			right = "Дальше ›"
		}
		a.ui.bottom(img, left, "", right)
		if err := a.show(img); err != nil {
			return -1, err
		}
		t, err := a.nextTap()
		if err != nil {
			return -1, err
		}
		switch {
		case t.y < 90 && t.x < width/2:
			return -1, nil
		case t.y >= 925 && t.x < width/3 && start >= 7:
			start -= 7
		case t.y >= 925 && t.x > width*2/3 && start+7 < len(items):
			start += 7
		case t.y >= 210 && t.y < 889:
			i := start + (t.y-210)/97
			if i < len(items) {
				return i, nil
			}
		}
	}
}

func (a *app) browseAuthor(p author) error {
	if p.UUID == "" {
		if p.Name == "" {
			return a.note("Автор не указан", "У этой книги нет автора в каталоге.", "Закрыть")
		}
		books, _, err := a.client.searchCatalog(p.Name, "")
		if err != nil {
			return a.note("Поиск недоступен", err.Error(), "Закрыть")
		}
		var matching []book
		for _, b := range books {
			if strings.Contains(strings.ToLower(b.authors()), strings.ToLower(p.Name)) {
				matching = append(matching, b)
			}
		}
		return a.pickBook(p.Name, matching)
	}
	for page := 1; ; page++ {
		books, err := a.client.authorBooks(p, page)
		if err != nil {
			return a.note("Книги автора недоступны", err.Error(), "Закрыть")
		}
		if len(books) == 0 {
			return a.note(p.Name, "Больше книг нет.", "Закрыть")
		}
		items := bookTitles(books)
		if len(books) == 20 {
			items = append(items, "Следующие книги автора")
		}
		i, err := a.pick(p.Name, items)
		if err != nil || i < 0 {
			return err
		}
		if i < len(books) {
			return a.card(books[i])
		}
	}
}

func bookTitles(books []book) []string {
	items := make([]string, len(books))
	for i, b := range books {
		items[i] = b.title()
		if n := localSeries(b).Number; n > 0 {
			items[i] = fmt.Sprintf("%g. %s", n, items[i])
		}
	}
	return items
}

func (a *app) pickBook(title string, books []book) error {
	if len(books) == 0 {
		return a.note(title, "Книги не найдены.", "Закрыть")
	}
	i, err := a.pick(title, bookTitles(books))
	if err != nil || i < 0 {
		return err
	}
	return a.card(books[i])
}

func (a *app) browseSeries(b book) error {
	series := localSeries(b)
	if series.title() == "" {
		return a.note("Серия не указана", "Яндекс не прислал сведения о серии для этой книги.", "Закрыть")
	}
	books := append([]book(nil), series.Books...)
	seen := map[string]bool{}
	for _, v := range books {
		seen[v.id()] = true
	}
	sources := append(append([]book{b}, a.shelf.Books...), a.shelf.Catalog.Books...)
	if p := b.firstAuthor(); p.UUID != "" {
		if related, err := a.client.authorBooks(p, 1); err == nil {
			sources = append(sources, related...)
		}
	}
	for _, item := range sources {
		if seen[item.id()] {
			continue
		}
		s := localSeries(item)
		if series.UUID != "" && series.UUID == s.UUID || s.title() != "" && strings.EqualFold(s.title(), series.title()) {
			seen[item.id()] = true
			books = append(books, item)
		}
	}
	numbers := make(map[string]float64, len(books))
	for _, item := range books {
		numbers[item.id()] = localSeries(item).Number
	}
	sort.SliceStable(books, func(i, j int) bool {
		first, second := numbers[books[i].id()], numbers[books[j].id()]
		return first > 0 && (second == 0 || first < second)
	})
	if len(books) == 0 {
		return a.note(series.title(), "Книги серии не найдены.", "Закрыть")
	}
	items := bookTitles(books)
	next := -1
	if series.Number > 0 {
		for i, item := range books {
			if numbers[item.id()] > series.Number {
				next = i
				break
			}
		}
	}
	if next >= 0 {
		items = append([]string{"Следующий том · " + books[next].title()}, items...)
	}
	i, err := a.pick(series.title(), items)
	if err != nil || i < 0 {
		return err
	}
	if next >= 0 {
		if i == 0 {
			return a.openKOReader(books[next])
		}
		i--
	}
	return a.card(books[i])
}

func (a *app) runQueue() error {
	if len(a.shelf.Queue) == 0 {
		return a.note("Очередь пуста", "Добавь книги из их карточек.", "Закрыть")
	}
	ok, err := a.confirm("Скачать очередь?", fmt.Sprintf("Книг в очереди: %d", len(a.shelf.Queue)), "Скачать")
	if err != nil || !ok {
		return err
	}
	saved := 0
	for _, b := range append([]book(nil), a.shelf.Queue...) {
		_, err := a.downloadForReader(b)
		if errors.Is(err, errExit) {
			return err
		}
		if errors.Is(err, context.Canceled) {
			break
		}
		if err != nil {
			if err := a.note("Ошибка скачивания", b.title()+" · "+err.Error(), "Продолжить"); err != nil {
				return err
			}
			continue
		}
		a.shelf.Queue = toggleBook(a.shelf.Queue, b)
		if !hasBook(a.shelf.Books, b.id()) {
			a.putShelfBook(b)
		}
		if err := a.saveShelf(); err != nil {
			return err
		}
		saved++
	}
	return a.note("Очередь обработана", fmt.Sprintf("Скачано: %d. Осталось в очереди: %d.", saved, len(a.shelf.Queue)), "К полке")
}
