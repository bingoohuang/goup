package goup

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bingoohuang/gg/pkg/man"
	"github.com/segmentio/ksuid"
)

func serveNormalUpload(w http.ResponseWriter, r *http.Request, chunkSize uint64) error {
	r.Body = http.MaxBytesReader(w, r.Body, int64(chunkSize))

	return NetHTTPUpload(w, r)
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
	File     string
	FileSize string
}

// NetHTTPUpload upload
func NetHTTPUpload(w http.ResponseWriter, r *http.Request) error {
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

	log.Printf("recieved file %s", file)

	return writeJSON(w, uploadResult{
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

	filename := firstFilename(filepath.Base(urlPath), filepath.Base(formFile.Filename), ksuid.New().String())
	fullPath := filepath.Join(RootDir, filename)

	// use temporary file directly
	if f, ok := file.(*os.File); ok {
		n, err := file.Seek(0, io.SeekEnd)
		if err != nil {
			return "", n, err
		}
		if err := file.Close(); err != nil {
			return "", 0, err
		}
		if err := os.Rename(f.Name(), fullPath); err != nil {
			return "", 0, err
		}
		return fullPath, n, nil
	}

	n, err := writeChunk(fullPath, file, nil)
	if err := file.Close(); err != nil {
		return "", 0, err
	}
	return fullPath, n, err
}

func firstFilename(s ...string) string {
	for _, i := range s {
		if i != "" && i != "/" {
			return i
		}
	}

	return ""
}
