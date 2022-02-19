package goup

import (
	"bytes"
	"context"
	_ "embed" // embed
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/schollz/pake/v3"

	"github.com/bingoohuang/gg/pkg/jsoni"
)

//go:embed index.html
var indexPage []byte

// InitServer initializes the server.
func InitServer() error {
	return ensureDir(RootDir)
}

// ServerHandle is main request/response handler for HTTP server.
func ServerHandle(chunkSize uint64, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := r.Header.Get(SessionID)
		cr := r.Header.Get(ContentRange)
		contentCurve := r.Header.Get(ContentCurve)
		log.Printf("[%s] %s", r.Method, r.URL.Path)

		switch {
		case r.URL.Path == "/pushfile" && r.Method == http.MethodPost:
			if err := servePushFile(w, r); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
			}
		case contentCurve != "" && r.Method == http.MethodPost:
			if sessionID == "" {
				w.WriteHeader(http.StatusNotFound)
			}
			if err := servePake(w, sessionID, code, contentCurve); err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
			}
		case r.URL.Path == "/" && cr != "":
			if sessionID == "" {
				w.WriteHeader(http.StatusNotFound)
			}
			if err := serveUpload(w, r, cr, sessionID); err != nil {
				log.Printf("uploading error: %v", err)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(err.Error()))
			}
		case r.URL.Path == "/" && r.Method == http.MethodGet:
			if r.Header.Get("Accept") == "apllication/json" {
				servList(w)
			} else {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(indexPage)
			}
		case r.Method == http.MethodGet: // may be downloads
			if sessionID == "" {
				w.WriteHeader(http.StatusNotFound)
			}
			if status := serveDownload(w, r, sessionID, cr, chunkSize); status > 0 {
				w.WriteHeader(status)
			}
		case r.Method == http.MethodPost:
			serveNormalUpload(w, r, chunkSize)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

var pakeCache = sync.Map{}

func setSessionKey(sessionID string, sessionKey []byte) {
	pakeCache.Store(sessionID, sessionKey)
}

func getSessionKey(sessionID string) []byte {
	sessionKey, ok := pakeCache.Load(sessionID)
	if !ok {
		return nil
	}

	if d, ok := sessionKey.([]byte); ok {
		return d
	}

	return nil
}

func servePake(w http.ResponseWriter, sessionID, code, contentCurve string) error {
	a, err := base64.RawURLEncoding.DecodeString(contentCurve)
	if err != nil {
		return fmt.Errorf("base64 decode error: %w", err)
	}

	b, err := pake.InitCurve([]byte(code), 1, "siec")
	if err != nil {
		return fmt.Errorf("init curve error: %w", err)
	}

	if err := b.Update(a); err != nil {
		return fmt.Errorf("update b error: %w", err)
	}

	bb := b.Bytes()
	bk, err := b.SessionKey()
	if err != nil {
		return err
	}

	setSessionKey(sessionID, bk)
	w.Header().Set(ContentCurve, base64.RawURLEncoding.EncodeToString(bb))
	return nil
}

// Entry is the file item for list.
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

func servList(w http.ResponseWriter) {
	var entries []Entry
	if err := filepath.WalkDir(RootDir, func(p string, d fs.DirEntry, err error) error {
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
		log.Printf("walk dir %s, error: %v", RootDir, err)
	}
	_ = jsoni.NewEncoder(w).Encode(context.Background(), entries)
}

func serveDownload(w http.ResponseWriter, r *http.Request, sessionID, contentRange string, chunkSize uint64) int {
	fullPath := filepath.Join(RootDir, "."+r.URL.Path)
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

	if sum := r.Header.Get(ContentChecksum); sum != "" {
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

	salt := genSalt()
	key, _, err := NewKey(getSessionKey(sessionID), salt)
	if err != nil {
		log.Printf("new key error: %v", err)
		return http.StatusInternalServerError
	}

	data, err := Encrypt(chunk, key)
	if err != nil {
		log.Printf("encrypt chunk error: %v", err)
		return http.StatusInternalServerError
	}

	w.Header().Set(ContentType, "application/octet-stream")
	w.Header().Set(ContentDisposition, mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set(ContentRange, contentRange)
	w.Header().Set(ContentSalt, base64.RawURLEncoding.EncodeToString(salt))
	log.Printf("send file %s with session %s, range %s", filename, r.Header.Get(SessionID), contentRange)

	_, _ = w.Write(data)
	return 0
}

func servePushFile(w http.ResponseWriter, r *http.Request) error {
	fullPath := filepath.Join(RootDir, r.Header.Get("Content-Filename"))
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		return fmt.Errorf("open file %s error: %w", fullPath, err)
	}
	defer Close(f)

	if _, err := io.Copy(f, r.Body); err != nil {
		return fmt.Errorf("write file %s error: %w", fullPath, err)
	}

	log.Printf("file pushed %s", fullPath)
	return nil
}

func serveUpload(w http.ResponseWriter, r *http.Request, contentRange, sessionID string) error {
	cr, err := parseContentRange(contentRange)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", contentRange, err)
	}
	_, params, err := mime.ParseMediaType(r.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}

	filename := params["filename"]
	fullPath := filepath.Join(RootDir, filename)

	if sum := r.Header.Get(ContentChecksum); r.Method == http.MethodGet && sum != "" {
		if old := readChecksum(fullPath, cr.From, cr.To); old == sum {
			w.WriteHeader(http.StatusNotModified)
		}

		return nil
	}

	if r.Method != http.MethodPost {
		return fmt.Errorf("invalid http method")
	}

	var body bytes.Buffer
	if _, err := io.Copy(&body, r.Body); err != nil {
		return err
	}

	salt, err := base64.RawURLEncoding.DecodeString(r.Header.Get(ContentSalt))
	if err != nil {
		return err
	}
	key, _, err := NewKey(getSessionKey(sessionID), salt)
	data, err := Decrypt(body.Bytes(), key)
	if err != nil {
		return fmt.Errorf("failed to decrypt: %w", err)
	}

	if _, err := writeChunk(fullPath, bytes.NewReader(data), cr); err != nil {
		return fmt.Errorf("open file %s error: %w", fullPath, err)
	}

	if _, err := w.Write([]byte(contentRange)); err != nil {
		return fmt.Errorf("write file %s error: %w", fullPath, err)
	}

	log.Printf("recv file %s with session %s, range %s", filename, sessionID, contentRange)
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
