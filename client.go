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
	"runtime"
	"sync"
)

type stateCode int

const (
	stopped stateCode = iota
	paused
	running
)

// GoUpload structure
type GoUpload struct {
	client    *http.Client
	url       string
	filePath  string
	ID        string
	chunkSize uint64
	file      *os.File
	channel   chan stateCode
	Status    UploadStatus
	wg        sync.WaitGroup
}

// UploadStatus holds the data about upload
type UploadStatus struct {
	Size             uint64
	SizeTransferred  uint64
	Parts            uint64
	PartsTransferred uint64
}

// New creates new instance of GoUpload Client
func New(url, filePath string, client *http.Client, chunkSize uint64) *GoUpload {
	g := &GoUpload{
		client:    client,
		url:       url,
		filePath:  filePath,
		ID:        generateSessionID(),
		chunkSize: chunkSize,
	}
	g.init()

	return g
}

// Init method initializes upload
func (c *GoUpload) init() {
	fileStat, err := os.Stat(c.filePath)
	checkError(err)

	c.Status.Size = uint64(fileStat.Size())
	c.Status.Parts = uint64(math.Ceil(float64(c.Status.Size) / float64(c.chunkSize)))

	c.channel = make(chan stateCode, 1)
	c.file, err = os.Open(c.filePath)
	checkError(err)
	c.wg.Add(1)

	go c.upload()
}

// Start set upload stateCode to uploading
func (c *GoUpload) Start() {
	c.channel <- running
}

// Pause set upload stateCode to paused
func (c *GoUpload) Pause() {
	c.channel <- paused
}

// Cancel set upload stateCode to stopped
func (c *GoUpload) Cancel() {
	c.channel <- stopped
}

func (c *GoUpload) upload() {
	defer c.file.Close()
	defer c.wg.Done()

	state := paused

	for i := uint64(0); i < c.Status.Parts; {
		select {
		case state = <-c.channel:
			switch state {
			case stopped:
				return
			case running:
			case paused:
			}
		default:
			runtime.Gosched()
			if state == paused {
				break
			}

			c.uploadChunk(i)
			i++
		}
	}
}

func (c *GoUpload) uploadChunk(i uint64) {
	partSize := Min(c.chunkSize, c.Status.Size-i*c.chunkSize)
	if partSize <= 0 {
		return
	}

	partBuffer := make([]byte, partSize)
	n, err := c.file.Read(partBuffer)
	checkError(err)
	if uint64(n) != partSize {
		log.Fatalf("read n %d, should be %d", n, partSize)
	}

	fileName := filepath.Base(c.filePath)
	contentRange := generateContentRange(i, c.chunkSize, partSize, c.Status.Size)
	responseBody, err := httpRequest(c.client, partBuffer, c.url, c.ID, contentRange, fileName)

	c.Status.SizeTransferred = parseBody(responseBody)
	c.Status.PartsTransferred = i + 1
}

func Min(a, b uint64) uint64 {
	if a < b {
		return a
	}

	return b
}

func (c *GoUpload) Wait() {
	c.wg.Wait()
}

func httpRequest(client *http.Client, part []byte, url, sessionID, contentRange, fileName string) (string, error) {
	r, err := http.NewRequest("POST", url, bytes.NewBuffer(part))
	if err != nil {
		return "", err
	}

	r.Header.Add("Content-Type", "application/octet-stream")
	r.Header.Add("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fileName}))
	r.Header.Add("Content-Range", contentRange)
	r.Header.Add("Session-ID", sessionID)

	rr, err := client.Do(r)
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
