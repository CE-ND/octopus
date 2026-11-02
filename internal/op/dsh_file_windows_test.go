//go:build windows

package op

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestDshWindowsAtomicReplaceRetriesSharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profile.yml")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { time.Sleep(50 * time.Millisecond); windows.CloseHandle(handle); close(closed) }()
	temporary, err := stageDshFile(path, []byte("new"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(temporary)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := renameDshFile(ctx, temporary, path); err != nil {
		t.Fatal(err)
	}
	<-closed
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "new" {
		t.Fatal("replacement was not committed")
	}
}
