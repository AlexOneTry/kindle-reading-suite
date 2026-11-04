package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

type shelfState struct {
	Books         []book
	Recent        map[string]time.Time
	Account       string
	Wishlist      []book
	Queue         []book
	SearchHistory []string
	Catalog       catalogView
	ResumeCatalog bool
	Access        []string
}

func (a *app) loadShelf() {
	data, err := os.ReadFile(a.shelfFile)
	if err == nil {
		if err = json.Unmarshal(data, &a.shelf); err != nil {
			log.Print("полка: ", err)
			a.shelf = shelfState{}
		}
	}
	if a.shelf.Recent == nil {
		a.shelf.Recent = map[string]time.Time{}
	}
}

func (a *app) saveShelf() error {
	data, err := json.Marshal(a.shelf)
	if err != nil {
		return err
	}
	return atomicWrite(a.shelfFile, data)
}

func (a *app) markOpened(b book) error {
	a.shelf.Recent[b.id()] = time.Now()
	for _, local := range a.shelf.Books {
		if local.id() == b.id() {
			return a.saveShelf()
		}
	}
	a.shelf.Books = append(a.shelf.Books, b)
	return a.saveShelf()
}

func (a *app) refreshShelf() string {
	books, err := a.client.library()
	if err != nil {
		log.Print("полка: ", err)
		if errors.Is(err, errSession) {
			return "Вход отклонён · подключи через USB"
		}
		return "Офлайн · сохранённая полка"
	}
	seen := make(map[string]bool, len(books))
	for _, b := range books {
		seen[b.id()] = true
	}
	for _, b := range a.shelf.Books {
		if !seen[b.id()] && (downloadSize(b) > 0 || hasBook(a.shelf.Wishlist, b.id()) || hasBook(a.shelf.Queue, b.id())) {
			books = append(books, b)
		}
	}
	a.shelf.Books = books
	if a.shelf.Account == "" {
		a.shelf.Account, _ = a.client.account()
	}
	if err := a.saveShelf(); err != nil {
		log.Print("полка: ", err)
	}
	if a.shelf.Account == "" {
		return "Подключено"
	}
	return "Аккаунт: " + a.shelf.Account
}

func (a *app) browse() error {
	mode, start := 0, 0
	query := ""
	if os.Getenv("YANDEX_BOOKS_RETURN") == "1" && a.shelf.ResumeCatalog {
		a.shelf.ResumeCatalog = false
		if err := a.browseCatalog(true); err != nil {
			return err
		}
	}
	status := "Сохранённая полка"
	if a.shelf.Account != "" {
		status += " · " + a.shelf.Account
	}
	if len(a.shelf.Books) == 0 {
		if err := a.show(a.ui.message("Моя полка", "Загружаю книги…", "")); err != nil {
			return err
		}
		status = a.refreshShelf()
	}
	for {
		books := a.visibleBooks(query, mode)
		if start >= len(books) {
			start = 0
		}
		last := a.lastBook()
		if mode >= 4 || query != "" {
			last = book{}
		}
		if err := a.show(a.ui.library(books, start, query, mode, status, last)); err != nil {
			return err
		}
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		switch {
		case hit(t, shelfTabs[0]):
			mode, start, query = 0, 0, ""
		case hit(t, shelfTabs[1]):
			if err := a.browseCatalog(false); err != nil {
				return err
			}
		case hit(t, shelfTabs[2]):
			mode, start, query = 5, 0, ""
		case hit(t, shelfTabs[3]):
			mode, start, query = 4, 0, ""
		case hit(t, footerPrevious) && start >= shelfRows:
			start -= shelfRows
		case hit(t, footerNext) && start+shelfRows < len(books):
			start += shelfRows
		case hit(t, footerAction):
			if mode == 6 {
				if len(books) == 0 {
					continue
				}
				if err := a.runQueue(); err != nil {
					return err
				}
				continue
			}
			if mode == 4 || mode == 5 {
				continue
			}
			if err := a.show(a.ui.message("Моя полка", "Обновляю книги…", "")); err != nil {
				return err
			}
			status = a.refreshShelf()
		case hit(t, shelfSearch):
			updated, err := a.search(query)
			if err != nil {
				return err
			}
			query, start = updated, 0
		case hit(t, shelfFilter) && mode != 5:
			if mode == 4 || mode == 6 {
				if mode == 4 {
					mode = 6
				} else {
					mode = 4
				}
				start, query = 0, ""
			} else {
				index, err := a.pick("Порядок на полке", filterLabels[:4])
				if err != nil {
					return err
				}
				if index >= 0 {
					mode, start = index, 0
				}
			}
		case hit(t, shelfContinue) && last.id() != "":
			if err := a.openKOReader(last); err != nil {
				return err
			}
		default:
			for row := 0; row < shelfRows && start+row < len(books); row++ {
				area, actions := shelfRow(row)
				if !hit(t, area) {
					continue
				}
				b := books[start+row]
				var err error
				if hit(t, actions) {
					err = a.card(b)
				} else {
					err = a.openKOReader(b)
				}
				if err != nil {
					return err
				}
				break
			}
		}
	}
}

func (a *app) lastBook() book {
	var last book
	var when time.Time
	for _, b := range a.shelf.Books {
		if t := a.shelf.Recent[b.id()]; t.After(when) {
			last, when = b, t
		}
	}
	return last
}

func (a *app) visibleBooks(query string, mode int) []book {
	var result []book
	needle := strings.ToLower(strings.TrimSpace(query))
	source := a.shelf.Books
	if mode == 5 {
		source = a.shelf.Wishlist
	}
	if mode == 6 {
		source = a.shelf.Queue
	}
	for _, b := range source {
		if !strings.Contains(strings.ToLower(b.title()+" "+b.authors()), needle) {
			continue
		}
		if mode == 3 && (!a.shelf.Recent[b.id()].IsZero() || b.RemoteProgress > 0 || b.RemoteState == "finished" || b.RemoteState == "reading") {
			continue
		}
		if mode == 4 && downloadSize(b) == 0 {
			continue
		}
		result = append(result, b)
	}
	if mode == 1 {
		sort.SliceStable(result, func(i, j int) bool { return strings.ToLower(result[i].title()) < strings.ToLower(result[j].title()) })
	}
	if mode == 2 {
		sort.SliceStable(result, func(i, j int) bool { return a.shelf.Recent[result[i].id()].After(a.shelf.Recent[result[j].id()]) })
	}
	return result
}

var filterLabels = []string{"По умолчанию", "По названию", "Недавние", "Непрочитанные", "Скачанные", "Хочу прочитать", "Очередь"}

func (a *app) search(initial string) (string, error) {
	return a.searchKeyboard(initial, "Поиск в списке")
}

func (a *app) searchKeyboard(initial, title string) (string, error) {
	value := []rune(initial)
	latin := false
	for {
		rows := []string{"йцукенгшщзх", "фывапролджэ", "ячсмитьбюёъ", "1234567890"}
		if latin {
			rows = []string{"qwertyuiop", "asdfghjkl", "zxcvbnm", "1234567890"}
		}
		img := canvas()
		a.ui.top(img, "‹ Назад", "Выход")
		label(img, a.ui.heading, margin, 160, title, 12)
		buttonFrame(img, image.Rect(margin, 193, width-margin, 273), false)
		prompt := string(value) + "│"
		if len(value) == 0 {
			prompt = "Название книги или автор"
		}
		label(img, a.ui.normal, margin+18, 243, shorten(a.ui.normal, prompt, width-2*margin-36), 20)
		for row, letters := range rows {
			keys := []rune(letters)
			w := (width - 2*margin) / len(keys)
			for col, key := range keys {
				x, y := margin+col*w, 320+row*105
				box(img, x, y, w-4, 88, 232)
				label(img, a.ui.normal, x+12, y+55, string(key), 22)
			}
		}
		for i, name := range []string{"РУ / EN", "Пробел", "Стереть"} {
			x := margin + i*224
			box(img, x, 750, 214, 92, 224)
			label(img, a.ui.small, x+18, 805, name, 22)
		}
		a.ui.actionsFooter(img, "Очистить", "Найти", "")
		if err := a.show(img); err != nil {
			return initial, err
		}
		t, err := a.nextTap()
		if err != nil {
			return initial, err
		}
		switch {
		case t.y < 90 && t.x < width/2:
			return initial, nil
		case hit(t, footerPrevious):
			value = nil
		case hit(t, footerAction):
			return string(value), nil
		case t.y >= 320 && t.y < 740 && t.x >= margin && t.x < width-margin:
			keys := []rune(rows[(t.y-320)/105])
			w := (width - 2*margin) / len(keys)
			col := (t.x - margin) / w
			if col < len(keys) && len(value) < 64 {
				value = append(value, keys[col])
			}
		case t.y >= 750 && t.y < 842 && t.x >= margin:
			switch (t.x - margin) / 224 {
			case 0:
				latin = !latin
			case 1:
				if len(value) < 64 {
					value = append(value, ' ')
				}
			case 2:
				if len(value) > 0 {
					value = value[:len(value)-1]
				}
			}
		}
	}
}

func downloadSize(b book) int64 {
	path, err := bookFile(koReaderBooks, b, false)
	if err != nil {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func (a *app) confirm(title, body, right string) (bool, error) {
	img := a.ui.message(title, body, "")
	a.ui.bottom(img, "К полке", "", right)
	if err := a.show(img); err != nil {
		return false, err
	}
	for {
		t, err := a.nextTap()
		if err != nil {
			return false, err
		}
		if t.y >= 925 {
			return t.x > width*2/3, nil
		}
	}
}

func (a *app) removeDownload(b book) error {
	path, err := bookFile(koReaderBooks, b, false)
	if err != nil {
		return err
	}
	yes, err := a.confirm("Освободить место?", "Файл книги будет удалён. Заметки и позиция KOReader сохранятся.", "Удалить EPUB")
	if err != nil || !yes {
		return err
	}
	// For books with no local .sdr, leave a marker to retain their filename.
	if err := os.MkdirAll(strings.TrimSuffix(path, ".epub")+".sdr", 0755); err != nil {
		return a.note("Не удалось", err.Error(), "К полке")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return a.note("Не удалось", err.Error(), "К полке")
	}
	return nil
}

func (a *app) downloadForReader(b book) (string, error) {
	type result struct {
		path string
		err  error
	}
	type amount struct{ done, total int64 }
	results := make(chan result, 1)
	updates := make(chan amount, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.show(a.ui.message("Открываю в KOReader", b.title(), "Отмена")); err != nil {
		return "", err
	}
	go func() {
		path, err := saveKOReaderBook(koReaderBooks, b, func(b book) ([]byte, error) {
			return a.client.downloadWithProgress(ctx, b, func(done, total int64) {
				select {
				case updates <- amount{done, total}:
				default:
				}
			})
		})
		results <- result{path, err}
	}()
	var last time.Time
	for {
		select {
		case r := <-results:
			return r.path, r.err
		case err := <-a.done():
			cancel()
			<-results
			return "", err
		case t := <-a.taps():
			if t.y >= 925 || t.y < 90 && t.x > width*3/4 {
				cancel()
				<-results
				if t.y < 90 {
					return "", errExit
				}
				return "", context.Canceled
			}
		case size := <-updates:
			if time.Since(last) < 1200*time.Millisecond {
				continue
			}
			last = time.Now()
			text := fmt.Sprintf("%.1f МБ", float64(size.done)/(1<<20))
			if size.total > 0 {
				text += fmt.Sprintf(" · %d%%", min(100, 100*size.done/size.total))
			}
			if err := a.show(a.ui.message("Загружаю книгу", b.title()+"   ·   "+text, "Отмена")); err != nil {
				cancel()
				<-results
				return "", err
			}
		}
	}
}
