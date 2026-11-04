package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type touch struct{ x, y int }

type display interface {
	show(*image.Gray) error
	taps() <-chan touch
	done() <-chan error
}

var errExit = errors.New("выход")

func readToken(dir, state string) (string, error) {
	pair := filepath.Join(dir, "pairing.txt")
	secret := filepath.Join(state, "session")
	for _, file := range []string{pair, secret} {
		data, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		value := strings.TrimSpace(string(data))
		if len(value) < 20 || len(value) > 4096 || strings.ContainsAny(value, "\r\n\t ") {
			return "", errors.New("неверный токен. Повтори настройку через USB.")
		}
		if file == pair {
			if err := atomicWrite(secret, []byte(value)); err != nil {
				return "", err
			}
			if err := os.Remove(pair); err != nil {
				return "", fmt.Errorf("удали pairing.txt через USB: %w", err)
			}
		}
		return value, nil
	}
	return "", errors.New("подключи Kindle по USB и запусти команду подключения на компьютере")
}

func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, writeErr := f.Write(data)
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(f.Name(), path)
}

type app struct {
	display
	ui            *ui
	client        *api
	progress      string
	handoff       string
	shelfFile     string
	shelf         shelfState
	returnCatalog bool
	openNotes     bool
}

func (a *app) nextTap() (touch, error) {
	select {
	case t := <-a.taps():
		if t.y < 90 && t.x > width*3/4 {
			return touch{}, errExit
		}
		return t, nil
	case err := <-a.done():
		return touch{}, err
	case <-time.After(15 * time.Minute):
		return touch{}, errExit
	}
}

func (a *app) note(title, body, action string) error {
	if err := a.show(a.ui.message(title, body, action)); err != nil {
		return err
	}
	for {
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		if t.y >= 925 {
			return nil
		}
	}
}

func (a *app) run(dir, state string) error {
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	token, err := readToken(dir, state)
	if err != nil {
		if err := a.note("Подключение", err.Error(), "Закрыть"); err != nil {
			return err
		}
		return nil
	}
	a.client, err = newAPI(token, filepath.Join(dir, "cacert.pem"))
	if err != nil {
		return a.note("Ошибка запуска", err.Error(), "Закрыть")
	}
	digest := sha256.Sum256([]byte(token))
	a.progress = filepath.Join(state, fmt.Sprintf("pages-%x", digest[:12]))
	if err := os.MkdirAll(a.progress, 0700); err != nil {
		return err
	}

	a.shelfFile = filepath.Join(a.progress, "shelf.json")
	a.loadShelf()
	return a.browse()
}

func (a *app) chooseReader(b book) error {
	if err := a.show(a.ui.bookActions(b, downloadSize(b))); err != nil {
		return err
	}
	for {
		t, err := a.nextTap()
		if err != nil {
			return err
		}
		switch {
		case t.y < 90 && t.x < width/2:
			return nil
		case t.x >= margin && t.x < width-margin && t.y >= 380 && t.y < 492:
			return a.openKOReader(b)
		case t.x >= margin && t.x < width-margin && t.y >= 530 && t.y < 642:
			a.shelf.ResumeCatalog = a.returnCatalog
			if err := a.markOpened(b); err != nil {
				log.Print(err)
			}
			return a.open(b)
		case t.y >= 670 && t.y < 780 && downloadSize(b) > 0:
			return a.removeDownload(b)
		}
	}
}

const koReaderLauncher = "/mnt/us/koreader/koreader.sh"
const koReaderBooks = "/mnt/us/Yandex Books"

func (a *app) openKOReader(b book) error {
	if info, err := os.Stat(koReaderLauncher); err != nil || !info.Mode().IsRegular() {
		return a.note("KOReader не найден", "Проверь, что KOReader установлен в папку koreader на Kindle.", "К полке")
	}
	for {
		path, err := a.downloadForReader(b)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		if errors.Is(err, errExit) {
			return err
		}
		if err == nil {
			a.shelf.ResumeCatalog = a.returnCatalog
			if err := a.markOpened(b); err != nil {
				log.Print(err)
			}
			if a.openNotes {
				if err := atomicWrite(a.handoff+"-notes", []byte("1")); err != nil {
					return err
				}
			}
			if err := atomicWrite(a.handoff, []byte(path+"\n")); err != nil {
				return err
			}
			break
		}
		log.Print("KOReader: ", err)
		retry, readErr := a.confirm("Книга недоступна", err.Error(), "Повторить")
		if readErr != nil || !retry {
			return readErr
		}
	}
	return errExit
}

func (a *app) open(b book) error {
	if err := a.show(a.ui.message("Открываю книгу", b.title(), "")); err != nil {
		return err
	}
	data, err := a.client.download(b)
	if err != nil {
		log.Print("загрузка: ", err)
		return a.note("Книга недоступна", err.Error(), "К полке")
	}
	text, err := paragraphs(data)
	if err != nil {
		log.Print("EPUB: ", err)
		return a.note("Ошибка чтения", err.Error(), "К полке")
	}
	pages := a.ui.paginate(text)
	if len(pages) == 0 {
		return a.note("Ошибка чтения", "В книге нет текста.", "К полке")
	}
	// Tie a page number to the visible text, even when ZIP metadata changes.
	digest := sha256.New()
	for _, paragraph := range text {
		io.WriteString(digest, paragraph)
		io.WriteString(digest, "\n")
	}
	bookKey := sha256.Sum256([]byte(b.id()))
	file := filepath.Join(a.progress, fmt.Sprintf("%x", bookKey[:16]))
	signature := fmt.Sprintf("%x", digest.Sum(nil)[:12])
	page := 0
	if data, err := os.ReadFile(file); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) == 2 && fields[0] == signature {
			if saved, err := strconv.Atoi(fields[1]); err == nil {
				page = max(0, min(saved, len(pages)-1))
			}
		}
	}
	for {
		if err := a.show(a.ui.reader(b, pages, page)); err != nil {
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
		previous := page
		switch {
		case t.x < width/3 && page > 0:
			page--
		case t.x > width*2/3 && page+1 < len(pages):
			page++
		}
		if page != previous {
			if err := atomicWrite(file, []byte(fmt.Sprintf("%s %d\n", signature, page))); err != nil {
				log.Print("прогресс: ", err)
			}
		}
	}
}

func writeFrame(path string, img *image.Gray) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	err = enc.Encode(f, img)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func main() {
	dir := flag.String("dir", ".", "папка расширения")
	state := flag.String("state", "/var/local/yandex-books-pw2", "локальные данные")
	runtimeDir := flag.String("runtime", "", "временная папка запуска")
	fbink := flag.String("fbink", "", "путь к FBInk")
	preview := flag.String("preview", "", "создать PNG интерфейса в указанной папке")
	flag.Parse()
	u, err := newUI()
	if err != nil {
		log.Fatal(err)
	}
	if *preview != "" {
		if err := demo(u, *preview); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := runDevice(u, *dir, *state, *runtimeDir, *fbink); err != nil && !errors.Is(err, errExit) {
		log.Fatal(err)
	}
}

func demo(u *ui, dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	books := []book{
		{UUID: "demo-1", Title: "Пиковая дама", Authors: []byte(`[{"name":"Александр Пушкин"}]`)},
		{UUID: "demo-2", Title: "Белые ночи", Authors: []byte(`[{"name":"Фёдор Достоевский"}]`)},
		{UUID: "demo-3", Title: "Записки охотника", Authors: []byte(`[{"name":"Иван Тургенев"}]`)},
	}
	pages := u.paginate([]string{
		"Однажды играли в карты у конногвардейца Нарумова. Долгая зимняя ночь прошла незаметно. Сели ужинать в пятом часу утра. Те, которые остались в выигрыше, ели с большим аппетитом; прочие в рассеянности сидели перед пустыми своими приборами.",
		"— Что ты сделал, Сурин? — спросил хозяин.",
		"— Проиграл, по обыкновению. Надобно признаться, что я несчастлив: играю мирандолем, никогда не горячусь, ничем меня с толку не собьёшь, а всё проигрываю!",
		"— И ты ни разу не соблазнился? ни разу не поставил на руте? Твёрдость твоя для меня удивительна.",
	})
	books[0].Annotation = "Повесть о том, как тайна трёх карт меняет судьбу молодого офицера."
	frames := map[string]*image.Gray{
		"shelf.png":   u.library(books, 0, "", 0, "Сохранённая полка", books[0]),
		"catalog.png": u.catalog(books, 0, "Пушкин", true, false),
		"card.png": u.bookCard(books[0], nil, "Доступна", []string{
			"Читать в KOReader", "Добавить в «Мои книги» Яндекса", "В «Хочу прочитать»", "В очередь скачивания", "Другие книги автора", "Книги серии", "Заметки в KOReader",
		}),
	}
	for name, frame := range frames {
		if err := writeFrame(filepath.Join(dir, name), frame); err != nil {
			return err
		}
	}
	return writeFrame(filepath.Join(dir, "reader.png"), u.reader(books[0], pages, 0))
}
