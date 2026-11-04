package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const apiURL = "https://api.bookmate.yandex.net/api/v5"
const bookLimit = 24 << 20

var errSession = errors.New("Яндекс отклонил вход. Обнови подключение через USB.")

type api struct {
	token, base string
	client      *http.Client
}

type author struct{ Name, UUID string }
type book struct {
	UUID                 string
	ID                   json.RawMessage
	Title                string
	Name                 string
	Authors              json.RawMessage
	AuthorsObjects       json.RawMessage `json:"authors_objects"`
	RemoteProgress       float64
	RemoteKnown          bool
	RemoteState          string
	Annotation           string
	EditorAnnotation     string
	EditorAnnotationREST string `json:"editor_annotation"`
	Cover                coverImage
	Series               json.RawMessage
	SeriesName           string          `json:"series_name"`
	SeriesPosition       json.RawMessage `json:"series_position"`
	Levels               []string        `json:"subscriptionLevels"`
	LevelsREST           []string        `json:"subscription_levels"`
	Level                string          `json:"subscription_level"`
	CanRead              *bool           `json:"can_read"`
	OnYandex             bool
}

func (b book) id() string {
	if b.UUID != "" {
		return b.UUID
	}
	var id string
	if json.Unmarshal(b.ID, &id) == nil {
		return id
	}
	var number json.Number
	if json.Unmarshal(b.ID, &number) == nil {
		return number.String()
	}
	return ""
}

func (b book) title() string {
	for _, title := range []string{b.Title, b.Name} {
		if title != "" {
			return title
		}
	}
	return "Без названия"
}

func (b book) authors() string {
	for _, raw := range []json.RawMessage{b.Authors, b.AuthorsObjects} {
		var authors []author
		if json.Unmarshal(raw, &authors) != nil {
			continue
		}
		var names []string
		for _, a := range authors {
			if a.Name != "" {
				names = append(names, a.Name)
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ", ")
		}
	}
	return ""
}

func newAPI(token, caFile string) (*api, error) {
	bundle, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(bundle) {
		return nil, errors.New("не удалось прочитать сертификаты")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		IdleConnTimeout: 30 * time.Second,
	}
	return &api{token, apiURL, &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (a *api) get(ctx context.Context, path, accept string, limit int64) ([]byte, error) {
	return a.getProgress(ctx, path, accept, limit, nil)
}

func (a *api) getProgress(ctx context.Context, path, accept string, limit int64, progress func(int64, int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+path, nil)
	if err != nil {
		return nil, err
	}
	a.setHeaders(req)
	req.Header.Set("Accept", accept)
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("сеть или сертификат: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
	case 401:
		return nil, errSession
	case 403:
		return nil, errors.New("Яндекс отказал в доступе. Проверь аккаунт и подписку.")
	case 404:
		return nil, errors.New("Яндекс не нашёл книгу или изменил адрес API.")
	default:
		return nil, fmt.Errorf("Яндекс вернул HTTP %d", resp.StatusCode)
	}
	var source io.Reader = resp.Body
	if progress != nil {
		source = &downloadReader{Reader: source, total: resp.ContentLength, update: progress}
	}
	body, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("ответ превышает лимит размера")
	}
	return body, nil
}

func (a *api) library() ([]book, error) {
	const pageSize = 100
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var books []book
	seen := map[string]bool{}
	for offset := 0; offset < 10000; offset += pageSize {
		path := fmt.Sprintf("/profile/library_cards?limit=%d&offset=%d", pageSize, offset)
		data, err := a.get(ctx, path, "application/json", 4<<20)
		if err != nil {
			return nil, err
		}
		var page struct {
			Cards *[]struct {
				Book     *book
				TextBook *book    `json:"text_book"`
				Reading  *float64 `json:"reading_progress"`
				Progress *float64 `json:"progress"`
				State    string
			} `json:"library_cards"`
		}
		if err := json.Unmarshal(data, &page); err != nil || page.Cards == nil {
			return nil, errors.New("Яндекс вернул неизвестный формат библиотеки")
		}
		for _, card := range *page.Cards {
			b := card.Book
			if b == nil {
				b = card.TextBook
			}
			if b != nil && b.id() != "" && !seen[b.id()] {
				seen[b.id()] = true
				value := card.Reading
				if value == nil {
					value = card.Progress
				}
				if value != nil {
					b.RemoteKnown = true
					b.RemoteProgress = percentFromAPI(*value)
				}
				b.RemoteState = card.State
				b.OnYandex = true
				books = append(books, *b)
			}
		}
		if len(*page.Cards) < pageSize {
			return books, nil
		}
	}
	return nil, errors.New("Яндекс вернул слишком много страниц библиотеки")
}

func (a *api) download(b book) ([]byte, error) {
	return a.downloadWithProgress(context.Background(), b, nil)
}

type downloadReader struct {
	io.Reader
	done, total int64
	update      func(int64, int64)
}

func (r *downloadReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.done += int64(n)
	r.update(r.done, r.total)
	return n, err
}

func (a *api) account() (string, error) {
	data, err := a.get(context.Background(), "/profile", "application/json", 1<<20)
	if err != nil {
		return "", err
	}
	var p struct{ User struct{ Login, Name string } }
	if err := json.Unmarshal(data, &p); err != nil {
		return "", err
	}
	if p.User.Login != "" {
		return p.User.Login, nil
	}
	return p.User.Name, nil
}

func (a *api) downloadWithProgress(ctx context.Context, b book, progress func(int64, int64)) ([]byte, error) {
	data, err := a.getProgress(ctx, "/books/"+url.PathEscape(b.id())+"/content/v4", "*/*", bookLimit, progress)
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return nil, errors.New("Яндекс не вернул EPUB для этой книги")
	}
	return data, nil
}

func (a *api) setHeaders(req *http.Request) {
	req.Header.Set("Auth-Token", a.token)
	req.Header.Set("App-Language", "ru")
	req.Header.Set("App-Locale", "ru")
	req.Header.Set("App-Platform", "android")
	req.Header.Set("Bookmate-Version", "20200305")
	req.Header.Set("Device-Os", "Android")
	req.Header.Set("User-Agent", "okhttp/4.12.0")
}
