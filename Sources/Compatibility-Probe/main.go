// A separate, read-only compatibility probe. Does not change the reading app.
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type axis struct{ Value, Low, High, Fuzz, Flat, Resolution int32 }

func touchAvailable() bool {
	paths, _ := filepath.Glob("/dev/input/event*")
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		for _, pair := range [][2]uint16{{0x35, 0x36}, {0, 1}} {
			valid := true
			for _, code := range pair {
				var a axis
				_, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(0x80184540)+uintptr(code), uintptr(unsafe.Pointer(&a)))
				valid = valid && e == 0 && a.High > a.Low
			}
			if valid {
				f.Close()
				return true
			}
		}
		f.Close()
	}
	return false
}

func field(s, name string) string {
	m := regexp.MustCompile(`(?:^|[;\s])` + name + `=['"]?([^;'"\r\n]+)`).FindStringSubmatch(s)
	if len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func main() {
	report := map[string]interface{}{"schema": 1, "probe_version": "1.0.0", "runtime": runtime.GOARCH,
		"generated_at": time.Now().UTC().Format(time.RFC3339), "root": os.Geteuid() == 0,
		"touch": touchAvailable(), "fbink_ok": false}
	if data, err := os.ReadFile("/etc/prettyversion.txt"); err == nil {
		report["firmware"] = strings.TrimSpace(string(data))
	}
	if data, err := exec.Command("uname", "-m").Output(); err == nil {
		report["machine"] = strings.TrimSpace(string(data))
	}
	for _, candidate := range os.Args[1:] {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		data, err := exec.CommandContext(ctx, candidate, "-e").Output()
		cancel()
		if err != nil {
			continue
		}
		text := string(data)
		w, _ := strconv.Atoi(field(text, "viewWidth"))
		h, _ := strconv.Atoi(field(text, "viewHeight"))
		if w == 0 || h == 0 {
			continue
		}
		report["fbink_ok"], report["width"], report["height"] = true, w, h
		report["model"], report["platform"] = field(text, "deviceName"), field(text, "devicePlatform")
		break
	}
	// Serial numbers, credentials and full FBInk output are deliberately omitted.
	json.NewEncoder(os.Stdout).Encode(report)
}
