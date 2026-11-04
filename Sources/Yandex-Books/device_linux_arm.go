package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

type panel struct {
	fbink, frame string
	tap          chan touch
	stop         chan error
	draws        int
}

func (p *panel) show(img *image.Gray) error {
	if err := writeFrame(p.frame, img); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	args := []string{"-q", "-g", "file=" + p.frame + ",w=-1,h=-1", "-W", "GC16"}
	if p.draws%8 == 0 {
		args = append(args, "-f")
	}
	data, err := exec.CommandContext(ctx, p.fbink, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("FBInk: %w (%s)", err, data)
	}
	p.draws++
	return nil
}

func (p *panel) taps() <-chan touch { return p.tap }
func (p *panel) done() <-chan error { return p.stop }

func (p *panel) finish(err error) {
	select {
	case p.stop <- err:
	default:
	}
}

type axis struct{ value, low, high, fuzz, flat, resolution int32 }

func readAxis(f *os.File, code uint16) (axis, bool) {
	var a axis
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(0x80184540)+uintptr(code), uintptr(unsafe.Pointer(&a)))
	return a, e == 0 && a.high > a.low
}

func scale(v int32, a axis, size int) int {
	return min(size-1, max(0, int((int64(v)-int64(a.low))*int64(size-1)/(int64(a.high)-int64(a.low)))))
}

func grab(f *os.File) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), 0x40044590, 1)
	if e != 0 {
		return e
	}
	return nil
}

func openTouch() (*os.File, axis, axis, bool, error) {
	paths, _ := filepath.Glob("/dev/input/event*")
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		ax, xOK := readAxis(f, 0x35)
		ay, yOK := readAxis(f, 0x36)
		mt := xOK && yOK
		if !mt {
			ax, xOK = readAxis(f, 0)
			ay, yOK = readAxis(f, 1)
		}
		if xOK && yOK {
			if err := grab(f); err != nil {
				f.Close()
				return nil, axis{}, axis{}, false, fmt.Errorf("сенсор %s: %w", path, err)
			}
			return f, ax, ay, mt, nil
		}
		f.Close()
	}
	return nil, axis{}, axis{}, false, fmt.Errorf("сенсор Kindle не найден")
}

func event(f *os.File) (uint16, uint16, int32, error) {
	var buf [16]byte // timeval is 8 bytes on 32-bit Kindle Linux.
	_, err := io.ReadFull(f, buf[:])
	return binary.LittleEndian.Uint16(buf[8:10]), binary.LittleEndian.Uint16(buf[10:12]), int32(binary.LittleEndian.Uint32(buf[12:])), err
}

func (p *panel) readTouch(f *os.File, ax, ay axis, mt bool) {
	var x, y, slot int32
	var xSet, ySet, down, release bool
	for {
		kind, code, val, err := event(f)
		if err != nil {
			p.finish(err)
			return
		}
		switch {
		case kind == 3 && code == 0x2f:
			slot = val
		case kind == 3 && slot == 0 && ((mt && code == 0x35) || (!mt && code == 0)):
			x, xSet = val, true
		case kind == 3 && slot == 0 && ((mt && code == 0x36) || (!mt && code == 1)):
			y, ySet = val, true
		case (mt && kind == 3 && code == 0x39 && slot == 0) || (!mt && kind == 1 && code == 330):
			up := (mt && val < 0) || (!mt && val == 0)
			if up {
				release = down
				down = false
			} else {
				down = true
				release = false
			}
		case kind == 0 && code == 3:
			down, release, xSet, ySet = false, false, false, false
		case kind == 0 && code == 0 && release:
			release = false
			if xSet && ySet {
				select {
				case p.tap <- touch{scale(x, ax, width), scale(y, ay, height)}:
				default:
				}
			}
		}
	}
}

func runDevice(u *ui, dir, state, runtimeDir, fbink string) error {
	if runtimeDir == "" || fbink == "" {
		return fmt.Errorf("запусти приложение через KUAL")
	}
	info, err := exec.Command(fbink, "-e").Output()
	if err != nil {
		return fmt.Errorf("проверка FBInk: %w", err)
	}
	match := regexp.MustCompile(`\bviewWidth=([0-9]+);viewHeight=([0-9]+)\b`).FindSubmatch(info)
	if match == nil {
		return fmt.Errorf("FBInk не сообщил размеры экрана")
	}
	screenWidth, _ := strconv.Atoi(string(match[1]))
	screenHeight, _ := strconv.Atoi(string(match[2]))
	if screenWidth < 600 || screenHeight <= screenWidth || screenHeight > 4096 {
		return fmt.Errorf("нужна книжная ориентация экрана Kindle: %d×%d", screenWidth, screenHeight)
	}
	f, ax, ay, mt, err := openTouch()
	if err != nil {
		return err
	}
	defer f.Close()
	p := &panel{fbink: fbink, frame: filepath.Join(runtimeDir, "frame.png"), tap: make(chan touch, 1), stop: make(chan error, 1)}
	go p.readTouch(f, ax, ay, mt)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(signals)
	go func() { <-signals; p.finish(errExit) }()
	// The hardware button also leaves the app if a touch target is inaccessible.
	if power, err := os.Open("/dev/input/event0"); err == nil {
		defer power.Close()
		if grab(power) == nil {
			go func() {
				for {
					kind, code, val, err := event(power)
					if err != nil {
						return
					}
					if kind == 1 && code == 116 && val == 0 {
						p.finish(errExit)
						return
					}
				}
			}()
		}
	}
	a := app{display: p, ui: u, handoff: filepath.Join(runtimeDir, "open-koreader")}
	return a.run(dir, state)
}
