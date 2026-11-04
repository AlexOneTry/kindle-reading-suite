package main

import "testing"

func TestFBInkFields(t *testing.T) {
    data := "FBINK_VERSION='1.25.0';viewWidth=1236;viewHeight=1648;deviceName='PaperWhite 5';devicePlatform='Bellatrix';"
    for name, want := range map[string]string{"viewWidth":"1236", "viewHeight":"1648", "deviceName":"PaperWhite 5", "devicePlatform":"Bellatrix", "missing":""} {
        if got := field(data, name); got != want { t.Fatalf("%s: got %q want %q", name, got, want) }
    }
}
