package goup

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// GoUpload structure
type GoUpload struct {
	client             *http.Client
	url                string
	filePath           string
	ID                 string
	chunkSize          uint64
	file               *os.File
	Status             UploadStatus
	wg                 sync.WaitGroup
	contentDisposition string
	bearer             string
}

// UploadStatus holds the data about upload
type UploadStatus struct {
	Size             uint64
	SizeTransferred  uint64
	Parts            uint64
	PartsTransferred uint64
}

// New creates new instance of GoUpload Client
func New(url, filePath, rename string, client *http.Client, chunkSize uint64, bearer string) (*GoUpload, error) {
	fileName := rename
	if fileName == "" {
		fileName = filepath.Base(filePath)
	}

	g := &GoUpload{
		client:             client,
		url:                url,
		filePath:           filePath,
		contentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": fileName}),
		ID:                 generateSessionID(),
		chunkSize:          chunkSize,
		bearer:             bearerPrefix + bearer,
	}
	if err := g.init(); err != nil {
		return nil, err
	}

	return g, nil
}

// Init method initializes upload
func (c *GoUpload) init() error {
	fileStat, err := os.Stat(c.filePath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", c.filePath, err)
	}

	c.Status.Size = uint64(fileStat.Size())
	c.Status.Parts = uint64(math.Ceil(float64(c.Status.Size) / float64(c.chunkSize)))

	c.file, err = os.Open(c.filePath)
	if err != nil {
		return fmt.Errorf("open %s: %w", c.filePath, err)
	}
	c.wg.Add(1)

	go func() {
		defer Close(c.file)
		defer c.wg.Done()

		if err := c.upload(); err != nil {
			log.Printf("upload error: %v", err)
		}
	}()
	return nil
}

func (c *GoUpload) upload() error {
	for i := uint64(0); i < c.Status.Parts; i++ {
		if err := c.uploadChunk(i); err != nil {
			return err
		}
	}

	return nil
}

func (c *GoUpload) uploadChunk(i uint64) error {
	partSize := GetPartSize(c.Status.Size, c.chunkSize, i)
	if partSize <= 0 {
		return nil
	}

	partBuffer := make([]byte, partSize)
	n, err := c.file.Read(partBuffer)
	if err != nil {
		return fmt.Errorf("read %s: %w", c.file.Name(), err)
	}

	if uint64(n) != partSize {
		return fmt.Errorf("read n %d, should be %d", n, partSize)
	}

	contentRange := generateContentRange(i, c.chunkSize, partSize, c.Status.Size)
	responseBody, err := c.chunkUpload(partBuffer, c.url, c.ID, contentRange)
	if err != nil {
		return fmt.Errorf("chunk %d upload: %w", i+1, err)
	}

	c.Status.SizeTransferred, err = parseBodyAsSizeTransferred(responseBody)
	if err != nil {
		return fmt.Errorf("parse body as size transferred %s: %w", responseBody, err)
	}

	c.Status.PartsTransferred = i + 1
	return nil
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}

	return b
}

// Wait waits the upload complete
func (c *GoUpload) Wait() {
	c.wg.Wait()
}

func (c *GoUpload) chunkUpload(part []byte, url, sessionID, contentRange string) (string, error) {
	r0, err := http.NewRequest(http.MethodGet, url, nil)

	r0.Header.Set(Authorization, c.bearer)
	r0.Header.Set(ContentRange, contentRange)
	r0.Header.Set(ContentDisposition, c.contentDisposition)
	r0.Header.Set(SessionID, sessionID)
	sum := checksum(part)
	r0.Header.Set(ContentSha256, sum)
	rr0, err := c.client.Do(r0)
	if err != nil {
		return "", err
	}
	if rr0.StatusCode == http.StatusNotModified {
		return contentRange, nil
	}

	if rr0.StatusCode != 200 {
		return "", fmt.Errorf("bad status code: %d", rr0.StatusCode)
	}

	r, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(part))
	if err != nil {
		return "", err
	}

	r.Header.Set(Authorization, c.bearer)
	r.Header.Set(ContentType, "application/octet-stream")
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set(ContentRange, contentRange)
	r.Header.Set(SessionID, sessionID)
	rr, err := c.client.Do(r)
	if err != nil {
		return "", err
	}
	defer Close(rr.Body)

	body, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		return "", err
	}

	if rr.StatusCode != 200 {
		return "", fmt.Errorf("bad status code: %d", rr.StatusCode)
	}

	return string(body), nil
}
