package app

import (
	"errors"
	"io"
	"mime/multipart"
	"os"
)

const multipartDiskMemoryLimit int64 = 32 << 20

var (
	errUploadedFileEmpty    = errors.New("uploaded file is empty")
	errUploadedFileTooLarge = errors.New("uploaded file is too large")
)

func copyMultipartFile(header *multipart.FileHeader, dstPath string, maxBytes int64) (int64, error) {
	file, err := header.Open()
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return copyMultipartStream(file, dstPath, maxBytes)
}

func copyMultipartStream(src multipart.File, dstPath string, maxBytes int64) (int64, error) {
	out, err := os.Create(dstPath)
	if err != nil {
		return 0, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()

	reader := io.Reader(src)
	if maxBytes > 0 {
		reader = io.LimitReader(src, maxBytes+1)
	}
	buf := make([]byte, 256<<10)
	n, err := io.CopyBuffer(out, reader, buf)
	if err != nil {
		return n, err
	}
	if maxBytes > 0 && n > maxBytes {
		return n, errUploadedFileTooLarge
	}
	if n == 0 {
		return 0, errUploadedFileEmpty
	}
	if err := out.Close(); err != nil {
		return n, err
	}
	closed = true
	return n, nil
}

func looksLikePDFFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 5)
	n, err := io.ReadFull(f, head)
	return err == nil && n == len(head) && string(head) == "%PDF-"
}
