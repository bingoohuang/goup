package goup

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

type fileStorage struct {
	Path string
}

// ServerFileStorage settings.
// When finished uploading with success files are stored inside Path config.
// While uploading temporary files are stored inside TempPath directory.
var ServerFileStorage = fileStorage{
	Path: "./.goup-files",
}

// InitServer initializes the server.
func InitServer() error {
	return ensureDir(ServerFileStorage.Path)
}

// UploadHandle is main request/response handler for HTTP server.
func UploadHandle(w http.ResponseWriter, r *http.Request) {
	if err := doUploadHandle(w, r); err != nil {
		log.Printf("uploading error: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(err.Error()))
	}
}

func doUploadHandle(w http.ResponseWriter, r *http.Request) error {
	header := r.Header.Get
	contentRange := header(ContentRange)
	if contentRange == "" {
		return fmt.Errorf("empty contentRange")
	}

	totalSize, partFrom, partTo, err := parseContentRange(contentRange)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", contentRange, err)
	}
	_, params, err := mime.ParseMediaType(header(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}

	filename := params["filename"]
	fullpath := filepath.Join(ServerFileStorage.Path, filename)
	contentSha256 := header(ContentSha256)
	if r.Method == http.MethodGet && contentSha256 != "" {
		if old := readChecksum(fullpath, partFrom, partTo); old == contentSha256 {
			w.WriteHeader(http.StatusNotModified)
		}

		return nil
	}

	if r.Method != http.MethodPost {
		return fmt.Errorf("invalid http method")
	}

	f, err := os.OpenFile(fullpath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		return fmt.Errorf("open file %s error: %w", fullpath, err)
	}
	defer Close(f)

	if partFrom == 0 {
		if err := f.Truncate(totalSize); err != nil {
			return fmt.Errorf("truncate file %s to size %d error: %w", fullpath, totalSize, err)
		}
	}

	sessionID := header(SessionID)
	if _, err := f.Seek(partFrom, io.SeekStart); err != nil {
		return fmt.Errorf("seek file %s with pot %d error: %w", f.Name(), partFrom, err)
	}
	if _, err := io.Copy(f, r.Body); err != nil {
		return fmt.Errorf("write file %s error: %w", fullpath, err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync file %s error: %w", fullpath, err)
	}

	if _, err := w.Write([]byte(contentRange)); err != nil {
		return fmt.Errorf("write file %s error: %w", fullpath, err)
	}

	log.Printf("recieved file %s with session %s, range %s", filename, sessionID, contentRange)
	return nil
}

func readChecksum(fullpath string, from, to int64) string {
	if fileNotExists(fullpath) {
		return ""
	}

	f, err := os.OpenFile(fullpath, os.O_RDONLY, 0o755)
	if err != nil {
		log.Printf("failed to open file %s,error: %v", fullpath, err)
		return ""
	}
	defer Close(f)

	if _, err := f.Seek(from, io.SeekStart); err != nil {
		log.Printf("failed to see file %s to %d ,error: %v", fullpath, from, err)
		return ""
	}

	buf := make([]byte, to-from)
	if _, err := f.Read(buf); err != nil {
		log.Printf("failed to read file %s  ,error: %v", fullpath, err)
		return ""
	}

	return checksum(buf)
}
