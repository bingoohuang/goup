package goup

import (
	"bytes"
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
}

// UploadStatus holds the data about upload
type UploadStatus struct {
	Size             uint64
	SizeTransferred  uint64
	Parts            uint64
	PartsTransferred uint64
}

// New creates new instance of GoUpload Client
func New(url, filePath, rename string, client *http.Client, chunkSize uint64) *GoUpload {
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
	}
	g.init()

	return g
}

// Init method initializes upload
func (c *GoUpload) init() {
	fileStat, err := os.Stat(c.filePath)
	checkError("stat %s error: %v", c.filePath, err)

	c.Status.Size = uint64(fileStat.Size())
	c.Status.Parts = uint64(math.Ceil(float64(c.Status.Size) / float64(c.chunkSize)))

	c.file, err = os.Open(c.filePath)
	checkError("stat %s error: %v", c.filePath, err)
	c.wg.Add(1)

	go c.upload()
}

func (c *GoUpload) upload() {
	defer c.file.Close()
	defer c.wg.Done()

	for i := uint64(0); i < c.Status.Parts; i++ {
		c.uploadChunk(i)
	}
}

func (c *GoUpload) uploadChunk(i uint64) {
	partSize := min(c.chunkSize, c.Status.Size-i*c.chunkSize)
	if partSize <= 0 {
		return
	}

	partBuffer := make([]byte, partSize)
	n, err := c.file.Read(partBuffer)
	checkError("read %s error: %v", c.file.Name(), err)
	if uint64(n) != partSize {
		log.Fatalf("read n %d, should be %d", n, partSize)
	}

	contentRange := generateContentRange(i, c.chunkSize, partSize, c.Status.Size)
	responseBody, err := c.chunkUpload(partBuffer, c.url, c.ID, contentRange)
	checkError("chunk %d upload error: %v", i+1, err)

	c.Status.SizeTransferred = parseBody(responseBody)
	c.Status.PartsTransferred = i + 1
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

	r0.Header.Add("Content-Range", contentRange)
	r0.Header.Add("Content-Disposition", c.contentDisposition)
	r0.Header.Add("Session-ID", sessionID)
	sum := checksum(part)
	r0.Header.Add("Content-Sha256", sum)
	rr0, err := c.client.Do(r0)
	if err != nil {
		return "", err
	}
	if rr0.StatusCode == http.StatusNotModified {
		return contentRange, nil
	}

	r, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(part))
	if err != nil {
		return "", err
	}
	r.Header.Add("Content-Type", "application/octet-stream")
	r.Header.Add("Content-Disposition", c.contentDisposition)
	r.Header.Add("Content-Range", contentRange)
	r.Header.Add("Session-ID", sessionID)
	rr, err := c.client.Do(r)
	if err != nil {
		return "", err
	}
	defer rr.Body.Close()

	body, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}
