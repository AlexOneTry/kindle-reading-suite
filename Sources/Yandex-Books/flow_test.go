package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixtureTransport func(http.ResponseWriter, *http.Request)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	f(recorder, r)
	return recorder.Result(), nil
}

func TestLibraryToEPUB(t *testing.T) {
	var contents bytes.Buffer
	archive := zip.NewWriter(&contents)
	for name, text := range map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="OPS/book.opf"/></rootfiles></container>`,
		"OPS/book.opf":           `<package><manifest><item id="a" href="two%20words.xhtml" media-type="application/xhtml+xml"/><item id="b" href="last.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/><itemref idref="b"/></spine></package>`,
		"OPS/two words.xhtml":    `<html><head><title>Не выводить</title></head><body><p>Первый <em>абзац</em>.</p><script>Не выводить</script><p>Второй &amp; последний.</p></body></html>`,
		"OPS/last.xhtml":         `<html><body><p>Конец книги.</p></body></html>`,
	} {
		w, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fmt.Fprint(w, text); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	transport := fixtureTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Auth-Token") != "fake-session" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/profile/library_cards":
			fmt.Fprint(w, `{"library_cards":[{"text_book":{"uuid":"test-book","id":123,"title":"Пример","authors":["someone"],"authors_objects":[{"name":"Автор"}]}}]}`)
		case "/books/test-book/content/v4":
			w.Write(contents.Bytes())
		default:
			http.NotFound(w, r)
		}
	})
	client := api{"fake-session", "https://fixture.invalid", &http.Client{Transport: transport}}
	books, err := client.library()
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 1 || books[0].title() != "Пример" || books[0].authors() != "Автор" {
		t.Fatalf("wrong shelf: %#v", books)
	}
	body, err := client.download(books[0])
	if err != nil {
		t.Fatal(err)
	}
	text, err := paragraphs(body)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(text, " / "); got != "Первый абзац. / Второй & последний. / Конец книги." {
		t.Fatalf("wrong EPUB order or text: %q", got)
	}
	u, err := newUI()
	if err != nil {
		t.Fatal(err)
	}
	pages := u.paginate(text)
	if len(pages) != 1 || pages[0][0] != "Первый абзац." {
		t.Fatalf("wrong pages: %v", pages)
	}
	dir := t.TempDir()
	saved, err := saveKOReaderBook(dir, books[0], client.download)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(saved)
	if err != nil {
		t.Fatalf("EPUB could not be read after transfer: %v", err)
	}
	repaired, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	if f := repaired.File[0]; f.Name != "mimetype" || f.Method != zip.Store || len(f.Extra) != 0 {
		t.Fatalf("bad first ZIP member: %v", f.FileHeader)
	}
	kind, err := fs.ReadFile(repaired, "mimetype")
	if err != nil || string(kind) != epubMediaType {
		t.Fatalf("bad EPUB marker: %q, %v", kind, err)
	}
	untouched, err := zip.NewReader(bytes.NewReader(contents.Bytes()), int64(contents.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range untouched.File {
		before, err := fs.ReadFile(untouched, f.Name)
		if err != nil {
			t.Fatal(err)
		}
		after, err := fs.ReadFile(repaired, f.Name)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("EPUB resource changed: %s (%v)", f.Name, err)
		}
	}
	// A download cached by v0.2.0 also needs repair at the same path.
	if err := os.WriteFile(saved, contents.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	books[0].Title = "Обновлённое название"
	reopened, err := saveKOReaderBook(dir, books[0], func(book) ([]byte, error) {
		t.Fatal("reopening must reuse the file associated with KOReader notes")
		return nil, nil
	})
	if err != nil || reopened != saved {
		t.Fatalf("book path changed: %q, %v", reopened, err)
	}
	migrated, err := os.ReadFile(saved)
	if err != nil || !bytes.Equal(migrated, original) {
		t.Fatalf("cached EPUB was not migrated at the same path: %v", err)
	}
	u.reader(books[0], pages, 0)
}

func TestLegacyEPUBMovesWithSidecar(t *testing.T) {
	dir := t.TempDir()
	b := book{UUID: "same-book", Title: "Автор: книга?"}
	old := filepath.Join(dir, bookKey(b))
	if err := os.WriteFile(old+".epub", []byte("epub"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(old+".sdr", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old+".sdr", "metadata.epub.lua"), []byte("notes"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := bookFile(dir, b, true)
	if err != nil || filepath.Base(file) != "Автор книга ["+bookKey(b)+"].epub" {
		t.Fatalf("bad migration: %q %v", file, err)
	}
	sidecar := strings.TrimSuffix(file, ".epub") + ".sdr"
	data, err := os.ReadFile(filepath.Join(sidecar, "metadata.epub.lua"))
	if err != nil || string(data) != "notes" {
		t.Fatalf("sidecar changed: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	b.Title = "Другое название"
	next, err := bookFile(dir, b, true)
	if err != nil || next != file {
		t.Fatalf("redownload would lose the sidecar: %q %v", next, err)
	}
}

func TestCatalogSearchUsesCursorAndFiltersAudio(t *testing.T) {
	transport := fixtureTransport(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.String() != catalogEndpoint || r.Header.Get("Auth-Token") != "fake-session" {
			t.Errorf("invalid catalog request: %s %s", r.Method, r.URL)
		}
		var payload struct {
			Variables struct {
				Query struct {
					Cursor, Query string
					Types         []string
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Variables.Query.Cursor != "cursor-before" || payload.Variables.Query.Query != "Пушкин" || payload.Variables.Query.Types == nil {
			t.Errorf("bad cursor or query: %v, %v", payload, err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"search":{"cursor":"cursor-after","page":[{"__typename":"AudioBook","book":{"uuid":"audio"}},{"__typename":"TextBook","book":{"uuid":"text","name":"Книга"},"progress":{"progress":0.4}}]}}}`)
	})
	client := api{token: "fake-session", client: &http.Client{Transport: transport}}
	results, cursor, err := client.searchCatalog("Пушкин", "cursor-before")
	if err != nil || len(results) != 1 || results[0].id() != "text" || results[0].RemoteProgress != 40 || cursor != "cursor-after" {
		t.Fatalf("wrong catalog response: %v %q %v", results, cursor, err)
	}
}

func TestAddToYandexSendsOnlySelectedBook(t *testing.T) {
	called := 0
	transport := fixtureTransport(func(w http.ResponseWriter, r *http.Request) {
		called++
		var body map[string]string
		if r.Method != http.MethodPost || r.URL.Path != "/profile/library_cards" || r.Header.Get("Auth-Token") != "fake-session" {
			t.Errorf("wrong request: %s %s", r.Method, r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["book_uuid"] != "selected" {
			t.Errorf("wrong selection: %v, %v", body, err)
		}
		w.WriteHeader(http.StatusCreated)
	})
	client := api{token: "fake-session", base: "https://fixture.invalid", client: &http.Client{Transport: transport}}
	if err := client.addToYandex(book{UUID: "selected"}); err != nil || called != 1 {
		t.Fatalf("request not accepted: %v, %d", err, called)
	}
}
