package main

import (
	"fmt"
	"image"
	"strings"

	"golang.org/x/image/font"
)

const shelfRows = 4

var (
	shelfTabs = [4]image.Rectangle{
		image.Rect(44, 164, 181, 224), image.Rect(189, 164, 332, 224),
		image.Rect(340, 164, 554, 224), image.Rect(562, 164, 714, 224),
	}
	shelfSearch    = image.Rect(44, 249, 379, 317)
	shelfFilter    = image.Rect(391, 249, 714, 317)
	shelfContinue  = image.Rect(44, 338, 714, 449)
	footerPrevious = image.Rect(44, 930, 223, 1001)
	footerAction   = image.Rect(238, 930, 520, 1001)
	footerNext     = image.Rect(535, 930, 714, 1001)
)

func hit(t touch, r image.Rectangle) bool { return image.Pt(t.x, t.y).In(r) }

func shelfRow(index int) (image.Rectangle, image.Rectangle) {
	y := 483 + index*106
	return image.Rect(44, y, 714, y+106), image.Rect(621, y+10, 714, y+96)
}

func buttonFrame(img *image.Gray, r image.Rectangle, primary bool) {
	if primary {
		box(img, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), 24)
		return
	}
	box(img, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), 116)
	box(img, r.Min.X+1, r.Min.Y+1, r.Dx()-2, r.Dy()-2, 249)
}

func (u *ui) button(img *image.Gray, r image.Rectangle, text string, primary bool) {
	if text == "" {
		return
	}
	buttonFrame(img, r, primary)
	ink := uint8(25)
	if primary {
		ink = 255
	}
	text = shorten(u.small, text, r.Dx()-20)
	label(img, u.small, r.Min.X+(r.Dx()-measure(u.small, text))/2, r.Min.Y+r.Dy()/2+8, text, ink)
}

func (u *ui) actionsFooter(img *image.Gray, left, center, right string) {
	box(img, margin, 914, width-2*margin, 1, 125)
	u.button(img, footerPrevious, left, false)
	u.button(img, footerAction, center, true)
	u.button(img, footerNext, right, false)
}

func (u *ui) library(books []book, start int, query string, mode int, status string, last book) *image.Gray {
	img := canvas()
	u.top(img, "YANDEX BOOKS", "Выход")
	u.center(img, 433, 56, shorten(u.small, status, 359))
	title, active := "Моя полка", 0
	switch mode {
	case 4:
		title, active = "Скачанные", 3
	case 5:
		title, active = "Хочу прочитать", 2
	case 6:
		title, active = "Очередь загрузок", 3
	}
	label(img, u.heading, margin, 139, title, 20)
	count := fmt.Sprintf("%d", len(books))
	label(img, u.small, width-margin-measure(u.small, count), 137, count, 65)
	for i, name := range []string{"Полка", "Каталог", "Хочу прочитать", "Загрузки"} {
		u.button(img, shelfTabs[i], name, i == active)
	}
	search := "Найти книгу"
	if query != "" {
		search = "Поиск: " + query
	}
	u.button(img, shelfSearch, search, false)
	switch mode {
	case 4:
		u.button(img, shelfFilter, "К очереди ›", false)
	case 6:
		u.button(img, shelfFilter, "‹ К скачанным", false)
	case 5:
	default:
		u.button(img, shelfFilter, filterLabels[mode]+" ›", false)
	}
	if last.id() != "" {
		buttonFrame(img, shelfContinue, true)
		label(img, u.small, margin+18, 376, "Продолжить чтение", 255)
		label(img, u.bold, margin+18, 422, shorten(u.bold, last.title(), width-2*margin-88), 255)
		label(img, u.bold, width-margin-42, 409, "›", 255)
	} else {
		message := "Нажми на книгу, чтобы открыть её"
		switch mode {
		case 4:
			message = "Эти книги доступны без интернета"
		case 5:
			message = "Добавляй книги из карточки через «Хочу прочитать»"
		case 6:
			message = "Добавляй книги в очередь из карточки"
		}
		label(img, u.small, margin, 395, shorten(u.small, message, width-2*margin), 60)
	}
	for i, b := range books[start:min(start+shelfRows, len(books))] {
		area, actions := shelfRow(i)
		sub := b.authors()
		var marks []string
		if count := annotationCount(b); count > 0 {
			marks = append(marks, fmt.Sprintf("Заметки: %d", count))
		}
		if size := downloadSize(b); size > 0 {
			marks = append(marks, fmt.Sprintf("%.1f МБ", float64(size)/(1<<20)))
		}
		if len(marks) > 0 {
			if sub != "" {
				sub += " · "
			}
			sub += strings.Join(marks, " · ")
		}
		u.listRow(img, area, actions, b.title(), sub)
	}
	if len(books) == 0 {
		message := "В этом списке пока нет книг"
		if query != "" {
			message = "По этому запросу ничего не найдено"
		}
		label(img, u.normal, margin, 543, message, 30)
	}
	left, action, right := "", "Обновить", ""
	if start > 0 {
		left = "‹ Назад"
	}
	if start+shelfRows < len(books) {
		right = "Дальше ›"
	}
	switch mode {
	case 4, 5:
		action = ""
	case 6:
		if len(books) > 0 {
			action = "Скачать очередь"
		} else {
			action = ""
		}
	}
	u.actionsFooter(img, left, action, right)
	return img
}

func (u *ui) listRow(img *image.Gray, area, actions image.Rectangle, title, subtitle string) {
	box(img, area.Min.X, area.Min.Y, area.Dx(), 1, 170)
	fit := actions.Min.X - area.Min.X - 18
	label(img, u.bold, area.Min.X, area.Min.Y+43, shorten(u.bold, title, fit), 18)
	label(img, u.small, area.Min.X, area.Min.Y+77, shorten(u.small, subtitle, fit), 65)
	buttonFrame(img, actions, false)
	x := actions.Min.X + actions.Dx()/2
	y := actions.Min.Y + actions.Dy()/2 - 2
	for i := -1; i <= 1; i++ {
		box(img, x+i*10-2, y, 5, 5, 29)
	}
}

func (u *ui) actionLabel(img *image.Gray, r image.Rectangle, text string, face font.Face, primary bool) {
	buttonFrame(img, r, primary)
	ink := uint8(25)
	if primary {
		ink = 255
	}
	label(img, face, r.Min.X+18, r.Min.Y+r.Dy()/2+8, shorten(face, text, r.Dx()-36), ink)
}
