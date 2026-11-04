//go:build !linux || !arm

package main

import "errors"

func runDevice(_ *ui, _, _, _, _ string) error {
	return errors.New("режим устройства предназначен для ARM Kindle. На компьютере используй -preview.")
}
