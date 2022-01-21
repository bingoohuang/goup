package goup

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bingoohuang/gg/pkg/jsoni"
)

type fileStorage struct {
	Path string
}

// FileStorage settings.
// When finished uploading with success files are stored inside Path config.
// While uploading temporary files are stored inside TempPath directory.
var FileStorage = fileStorage{
	Path: "./.goup-files",
}

// InitServer initializes the server.
func InitServer() error {
	return ensureDir(FileStorage.Path)
}

// ServerHandle is main request/response handler for HTTP server.
func ServerHandle(chunkSize uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cr := r.Header.Get(ContentRange)

		switch {
		case r.URL.Path == "/" && cr != "":
			if err := doUploadHandle(w, r, cr); err != nil {
				log.Printf("uploading error: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
			}
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			servList(w)
		case r.Method == http.MethodGet: // may be downloads
			if status := serveDownload(w, r, cr, chunkSize); status > 0 {
				w.WriteHeader(status)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

// Entry is the file item for list.
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func servList(w http.ResponseWriter) {
	var entries []Entry
	if err := filepath.WalkDir(FileStorage.Path, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		stat, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, Entry{
			Name: d.Name(),
			Size: stat.Size(),
		})
		return nil
	}); err != nil {
		log.Printf("walk dir %s, error: %v", FileStorage.Path, err)
	}
	_ = jsoni.NewEncoder(w).Encode(context.Background(), entries)
}

func serveDownload(w http.ResponseWriter, r *http.Request, contentRange string, chunkSize uint64) int {
	fullPath := filepath.Join(FileStorage.Path, "."+r.URL.Path)
	stat, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		return http.StatusNotFound
	}

	filename := filepath.Base(fullPath)
	if contentRange == "" {
		totalSize := uint64(stat.Size())
		partSize := GetPartSize(totalSize, chunkSize, 0)
		cr := newChunkRange(0, chunkSize, partSize, totalSize)
		w.Header().Set(ContentRange, cr.createContentRange())
		w.Header().Set(ContentDisposition, mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
		return 0
	}

	cr, err := parseContentRange(contentRange)
	if err != nil {
		log.Printf("parse contentRange %s error: %v", contentRange, err)
		return http.StatusInternalServerError
	}

	if sum := r.Header.Get(ContentSha256); sum != "" {
		if old := readChecksum(fullPath, cr.From, cr.To); old == sum {
			log.Printf("304 file %s with session %s, range %s", filename, r.Header.Get(SessionID), contentRange)
			return http.StatusNotModified
		}
	}

	chunk, err := readChunk(fullPath, cr.From, cr.To)
	if err != nil {
		log.Printf("read %s chunk: %v", fullPath, err)
		return http.StatusInternalServerError
	}

	w.Header().Set(ContentType, "application/octet-stream")
	w.Header().Set(ContentDisposition, mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set(ContentRange, contentRange)
	log.Printf("send file %s with session %s, range %s", filename, r.Header.Get(SessionID), contentRange)

	_, _ = w.Write(chunk)
	return 0
}

func doUploadHandle(w http.ResponseWriter, r *http.Request, contentRange string) error {
	cr, err := parseContentRange(contentRange)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", contentRange, err)
	}
	_, params, err := mime.ParseMediaType(r.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}

	filename := params["filename"]
	fullPath := filepath.Join(FileStorage.Path, filename)

	if sum := r.Header.Get(ContentSha256); r.Method == http.MethodGet && sum != "" {
		if old := readChecksum(fullPath, cr.From, cr.To); old == sum {
			w.WriteHeader(http.StatusNotModified)
		}

		return nil
	}

	if r.Method != http.MethodPost {
		return fmt.Errorf("invalid http method")
	}

	if err := writeChunk(fullPath, r.Body, cr); err != nil {
		return fmt.Errorf("open file %s error: %w", fullPath, err)
	}

	if _, err := w.Write([]byte(contentRange)); err != nil {
		return fmt.Errorf("write file %s error: %w", fullPath, err)
	}

	log.Printf("recv file %s with session %s, range %s", filename, r.Header.Get(SessionID), contentRange)
	return nil
}

func readChecksum(fullPath string, from, to uint64) string {
	if fileNotExists(fullPath) {
		return ""
	}

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0o755)
	if err != nil {
		log.Printf("failed to open file %s,error: %v", fullPath, err)
		return ""
	}
	defer Close(f)

	if _, err := f.Seek(int64(from), io.SeekStart); err != nil {
		log.Printf("failed to see file %s to %d ,error: %v", fullPath, from, err)
		return ""
	}

	buf := make([]byte, to-from)
	if n, err := f.Read(buf); err != nil {
		log.Printf("failed to read file %s  ,error: %v", fullPath, err)
		return ""
	} else if n < int(to-from) {
		log.Printf("read file %s not enough real %d < expected %d error: %v", fullPath, n, to-from, err)
		return ""
	}

	return checksum(buf)
}
