package invocation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

type Recorder interface {
	Append(Record) error
}

// FileRecorder appends one preparation record per line. A lock protects
// concurrent wrapper processes sharing the same local journal.
type FileRecorder struct {
	Path string
}

func (f FileRecorder) Append(record Record) error {
	if !filepath.IsAbs(f.Path) {
		return errors.New("record path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0700); err != nil {
		return err
	}
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	fd, err := syscall.Open(f.Path, syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), f.Path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("record journal must be a private regular file")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if _, err := file.Write(line); err != nil {
		return err
	}
	return file.Sync()
}
