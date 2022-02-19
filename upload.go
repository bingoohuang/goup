package goup

import (
	"encoding/json"
	"errors"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bingoohuang/gg/pkg/man"
	"github.com/segmentio/ksuid"
)

func serveNormalUpload(w http.ResponseWriter, r *http.Request, chunkSize uint64) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(chunkSize))

	if err := NetHTTPUpload(w, r); err != nil {
		log.Printf("error occurred: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) error {
	js, err := json.Marshal(v)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(js)
	return err
}

type uploadResult struct {
	CostTime string
	File     string
	FileSize string
}

// NetHTTPUpload upload
func NetHTTPUpload(w http.ResponseWriter, r *http.Request) error {
	start := time.Now()

	if err := r.ParseMultipartForm(16 /*16 MiB */ << 20); err != nil {
		return err
	}

	formFile, err := ParseFormFile(r.MultipartForm)
	if err != nil {
		return err
	}

	file, n, err := saveFormFile(formFile, r.URL.Path)
	if err != nil {
		return err
	}

	duration := time.Since(start)
	log.Printf("recieved file %s, cost time %s", file, duration)

	return writeJSON(w, uploadResult{
		CostTime: duration.String(),
		File:     file,
		FileSize: man.Bytes(uint64(n)),
	})
}

// ParseFormFile returns the first file for the provided form key.
// FormFile calls ParseMultipartForm and ParseForm if necessary.
func ParseFormFile(m *multipart.Form) (*multipart.FileHeader, error) {
	if m != nil {
		if fhs := m.File["file"]; len(fhs) > 0 {
			return fhs[0], nil
		}

		for _, v := range m.File {
			return v[0], nil
		}
	}

	return nil, ErrMissingFile
}

// ErrMissingFile may be returned from FormFile when the is no uploaded file.
var ErrMissingFile = errors.New("there is no uploaded file")

func saveFormFile(formFile *multipart.FileHeader, urlPath string) (string, int64, error) {
	file, err := formFile.Open()
	if err != nil {
		return "", 0, err
	}

	if f, ok := file.(*os.File); ok {
		defer func(name string) {
			log.Printf("remove tmpfile: %s", name)
			if err := os.Remove(name); err != nil {
				log.Printf("tmpfile: %s remove failed: %v", name, err)
			}
		}(f.Name())
	}

	defer file.Close()

	filename := firstNonEmpty(filepath.Base(urlPath), filepath.Base(formFile.Filename), ksuid.New().String())
	fullPath := filepath.Join(RootDir, filename)
	n, err := writeChunk(fullPath, file, nil)
	return fullPath, n, err
}

func firstNonEmpty(s ...string) string {
	for _, i := range s {
		if i != "" {
			return i
		}
	}

	return ""
}
