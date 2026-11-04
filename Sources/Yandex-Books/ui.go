package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	width        = 758
	height       = 1024
	margin       = 44
	shelfSize    = 6
	lineHeight   = 37
	linesPerPage = 20
)

type ui struct{ normal, small, bold, heading font.Face }

func newUI() (*ui, error) {
	regular, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	result := &ui{}
	for _, f := range []struct {
		target *font.Face
		source *opentype.Font
		size   float64
	}{
		{&result.normal, regular, 29}, {&result.small, regular, 23},
		{&result.bold, bold, 30}, {&result.heading, bold, 41},
	} {
		*f.target, err = opentype.NewFace(f.source, &opentype.FaceOptions{
			Size: f.size, DPI: 72, Hinting: font.HintingFull,
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func canvas() *image.Gray {
	img := image.NewGray(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	return img
}

func box(img *image.Gray, x, y, w, h int, grey uint8) {
	draw.Draw(img, image.Rect(x, y, x+w, y+h), image.NewUniform(color.Gray{grey}), image.Point{}, draw.Src)
}

func label(img *image.Gray, f font.Face, x, y int, s string, grey uint8) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(color.Gray{grey}), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func measure(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() }

func shorten(f font.Face, s string, maxWidth int) string {
	if measure(f, s) <= maxWidth {
		return s
	}
	var result []rune
	for _, r := range s {
		result = append(result, r)
		if measure(f, string(result)+"…") > maxWidth {
			return string(result[:len(result)-1]) + "…"
		}
	}
	return s
}

func wrap(f font.Face, text string, maxWidth int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" {
			if measure(f, line+" "+word) <= maxWidth {
				line += " " + word
				continue
			}
			lines = append(lines, line)
		}
		line = ""
		if measure(f, word) > maxWidth {
			for _, r := range word {
				if line != "" && measure(f, line+string(r)) > maxWidth {
					lines = append(lines, line)
					line = ""
				}
				line += string(r)
			}
		} else {
			line = word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (u *ui) paginate(paragraphs []string) [][]string {
	var pages [][]string
	page := make([]string, 0, linesPerPage)
	for _, p := range paragraphs {
		lines := wrap(u.normal, p, width-2*margin)
		for _, line := range lines {
			if len(page) == linesPerPage {
				pages = append(pages, page)
				page = make([]string, 0, linesPerPage)
			}
			page = append(page, line)
		}
		if len(page) > 0 && len(page) < linesPerPage-1 {
			page = append(page, "")
		}
	}
	if len(page) > 0 {
		pages = append(pages, page)
	}
	return pages
}

func (u *ui) center(img *image.Gray, x, baseline int, s string) {
	label(img, u.small, x-measure(u.small, s)/2, baseline, s, 24)
}

func (u *ui) top(img *image.Gray, left, right string) {
	if left != "" {
		label(img, u.small, margin, 57, left, 24)
	}
	if right != "" {
		label(img, u.small, width-margin-measure(u.small, right), 57, right, 24)
	}
	box(img, margin, 83, width-2*margin, 2, 35)
}

func (u *ui) bottom(img *image.Gray, left, center, right string) {
	box(img, margin, 925, width-2*margin, 2, 35)
	label(img, u.small, margin, 979, left, 24)
	u.center(img, width/2, 979, center)
	label(img, u.small, width-margin-measure(u.small, right), 979, right, 24)
}

func (u *ui) shelf(books []book, start int) *image.Gray {
	img := canvas()
	u.top(img, "ЯНДЕКС КНИГИ", "Выход")
	label(img, u.heading, margin, 153, "Моя полка", 12)
	for i, b := range books[start:min(start+shelfSize, len(books))] {
		y := 197 + i*117
		box(img, margin, y, width-2*margin, 1, 155)
		label(img, u.bold, margin, y+47, shorten(u.bold, b.title(), width-2*margin-28), 15)
		label(img, u.small, margin, y+81, shorten(u.small, b.authors(), width-2*margin-28), 65)
		label(img, u.small, width-margin-12, y+47, "›", 32)
	}
	if len(books) == 0 {
		label(img, u.normal, margin, 255, "Добавь книгу в «Мои книги» у Яндекса.", 24)
	}
	left, right := "", ""
	if start > 0 {
		left = "‹ Назад"
	}
	if start+shelfSize < len(books) {
		right = "Дальше ›"
	}
	u.bottom(img, left, "Обновить", right)
	return img
}

func (u *ui) bookActions(b book, size int64) *image.Gray {
	img := canvas()
	u.top(img, "‹ Карточка", "Выход")
	for i, line := range wrap(u.heading, b.title(), width-2*margin) {
		if i == 3 {
			break
		}
		label(img, u.heading, margin, 157+i*54, line, 12)
	}
	label(img, u.small, margin, 321, shorten(u.small, b.authors(), width-2*margin), 65)
	u.actionLabel(img, image.Rect(margin, 380, width-margin, 492), "Читать в KOReader", u.bold, true)
	u.actionLabel(img, image.Rect(margin, 530, width-margin, 642), "Читать в Yandex Books", u.bold, false)
	if size > 0 {
		u.actionLabel(img, image.Rect(margin, 670, width-margin, 780), fmt.Sprintf("Освободить %.1f МБ", float64(size)/(1<<20)), u.small, false)
	}
	if b.RemoteKnown {
		label(img, u.small, margin, 852, fmt.Sprintf("Прогресс в Яндексе: %.1f%%", min(100, max(0, b.RemoteProgress))), 65)
	}

	return img
}

func (u *ui) reader(b book, pages [][]string, page int) *image.Gray {
	img := canvas()
	u.top(img, "‹ Полка", "Выход")
	label(img, u.small, margin, 129, shorten(u.small, b.title(), width-2*margin), 65)
	for i, line := range pages[page] {
		label(img, u.normal, margin, 186+i*lineHeight, line, 13)
	}
	left, right := "", ""
	if page > 0 {
		left = "‹ Назад"
	}
	if page+1 < len(pages) {
		right = "Дальше ›"
	}
	u.bottom(img, left, fmt.Sprintf("%d / %d", page+1, len(pages)), right)
	return img
}

func (u *ui) message(title, body, action string) *image.Gray {
	img := canvas()
	u.top(img, "ЯНДЕКС КНИГИ", "Выход")
	label(img, u.heading, margin, 180, shorten(u.heading, title, width-2*margin), 12)
	for i, line := range wrap(u.normal, body, width-2*margin) {
		if i >= 16 {
			break
		}
		label(img, u.normal, margin, 250+i*42, line, 23)
	}
	u.bottom(img, "", action, "")
	return img
}
