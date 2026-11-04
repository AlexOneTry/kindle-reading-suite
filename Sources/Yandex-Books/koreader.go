package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func saveKOReaderBook(dir string, b book, download func(book) ([]byte, error)) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path, err := bookFile(dir, b, true)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	var data []byte
	cached := err == nil
	switch {
	case cached:
		if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > bookLimit+1024 {
			return "", errors.New("сохранённый EPUB повреждён")
		}
		data, err = os.ReadFile(path)
	case errors.Is(err, os.ErrNotExist):
		data, err = download(b)
	}
	if err != nil {
		return "", err
	}

	if !cached {
		if _, err := paragraphs(data); err != nil {
			return "", fmt.Errorf("в загруженной книге нет читаемых глав: %w", err)
		}
	}
	prepared, changed, err := prepareKOReaderEPUB(data)
	if err != nil {
		return "", err
	}
	if !cached || changed {
		if err := atomicWrite(path, prepared); err != nil {
			return "", err
		}
	}
	return path, nil
}

func bookKey(b book) string {
	key := sha256.Sum256([]byte(b.id()))
	return fmt.Sprintf("%x", key[:16])
}

func bookFile(dir string, b book, migrate bool) (string, error) {
	key := bookKey(b)
	legacy := filepath.Join(dir, key+".epub")
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, " ["+key+"].epub") {
			return filepath.Join(dir, name), nil
		}
	}
	// A retained sidecar fixes the name even after the EPUB is removed.
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && strings.HasSuffix(name, " ["+key+"].sdr") {
			target := filepath.Join(dir, strings.TrimSuffix(name, ".sdr")+".epub")
			if _, err := os.Stat(legacy); err == nil {
				if !migrate {
					return legacy, nil
				}
				if err := os.Rename(legacy, target); err != nil {
					return "", err
				}
			}
			return target, nil
		}
	}

	oldNotes := filepath.Join(dir, key+".sdr")
	_, oldFileErr := os.Stat(legacy)
	_, oldNotesErr := os.Stat(oldNotes)
	if oldFileErr == nil && (!migrate || oldNotesErr != nil) ||
		errors.Is(oldFileErr, os.ErrNotExist) && oldNotesErr == nil {
		// If the local sidecar isn't next to an existing file, KOReader may
		// store it centrally. Keep that file's identity.
		return legacy, nil
	}
	readable := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*[]`, r) {
			return ' '
		}
		return r
	}, b.title())
	readable = strings.Trim(strings.Join(strings.Fields(readable), " "), " .")
	if readable == "" {
		readable = "Книга"
	}
	runes := []rune(readable)
	if len(runes) > 45 {
		readable = string(runes[:45])
	}
	target := filepath.Join(dir, readable+" ["+key+"].epub")
	if oldFileErr == nil && oldNotesErr == nil && migrate {
		newNotes := strings.TrimSuffix(target, ".epub") + ".sdr"
		if err := os.Rename(oldNotes, newNotes); err != nil {
			return "", err
		}
		if err := os.Rename(legacy, target); err != nil {
			return "", errors.Join(err, os.Rename(newNotes, oldNotes))
		}
	}
	return target, nil
}

const epubMediaType = "application/epub+zip"

func prepareKOReaderEPUB(data []byte) ([]byte, bool, error) {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, false, errors.New("повреждённый архив EPUB")
	}
	if container, err := archive.Open("META-INF/container.xml"); err != nil {
		return nil, false, errors.New("в файле нет манифеста EPUB")
	} else {
		container.Close()
	}
	mime, err := archive.Open("mimetype")
	if err == nil {
		mime.Close()
		return data, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}

	// Some Bookmate downloads omit this ZIP member, which CREngine uses to
	// detect EPUB. Put it first, uncompressed, with no ZIP extra fields.
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	payload := []byte(epubMediaType)
	header := &zip.FileHeader{
		Name: "mimetype", Method: zip.Store, CRC32: crc32.ChecksumIEEE(payload),
		CompressedSize64: uint64(len(payload)), UncompressedSize64: uint64(len(payload)),
	}
	entry, err := writer.CreateRaw(header)
	if err != nil {
		return nil, false, err
	}
	if _, err := entry.Write(payload); err != nil {
		return nil, false, err
	}
	for _, member := range archive.File {
		if err := writer.Copy(member); err != nil {
			return nil, false, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, false, err
	}
	return buffer.Bytes(), true, nil
}
