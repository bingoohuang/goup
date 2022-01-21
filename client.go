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

	"github.com/bingoohuang/gg/pkg/rest"
)

// Client structure
type Client struct {
	client             *http.Client
	url                string
	fullPath           string
	ID                 string
	chunkSize          uint64
	TotalSize          uint64
	wg                 sync.WaitGroup
	contentDisposition string
	bearer             string
	progress           Progress
	coroutines         int
}

// GetParts get the number of chunk parts.
func (c *Client) GetParts() uint64 {
	return uint64(math.Ceil(float64(c.TotalSize) / float64(c.chunkSize)))
}

// Opt is the client options.
type Opt struct {
	ChunkSize uint64
	Progress
	*http.Client
	Rename     string
	Bearer     string
	FullPath   string
	Coroutines int
}

// OptFn is the option pattern func prototype.
type OptFn func(*Opt)

// WithHTTPClient set *http.Client.
func WithHTTPClient(v *http.Client) OptFn { return func(c *Opt) { c.Client = v } }

// WithChunkSize set ChunkSize.
func WithChunkSize(v uint64) OptFn { return func(c *Opt) { c.ChunkSize = v } }

// WithProgress set WithProgress.
func WithProgress(v Progress) OptFn { return func(c *Opt) { c.Progress = v } }

// WithRename set WithRename.
func WithRename(v string) OptFn { return func(c *Opt) { c.Rename = v } }

// WithBearer set Bearer.
func WithBearer(v string) OptFn { return func(c *Opt) { c.Bearer = v } }

// WithFullPath set FullPath.
func WithFullPath(v string) OptFn { return func(c *Opt) { c.FullPath = v } }

// WithCoroutines set Coroutines.
func WithCoroutines(v int) OptFn { return func(c *Opt) { c.Coroutines = v } }

// New creates new instance of Client.
func New(url string, fns ...OptFn) (*Client, error) {
	opt := &Opt{}
	for _, fn := range fns {
		fn(opt)
	}

	fileName := opt.Rename
	if fileName == "" && opt.FullPath != "" {
		fileName = filepath.Base(opt.FullPath)
	}

	if opt.Client == nil {
		opt.Client = &http.Client{}
	}
	if opt.Progress == nil {
		opt.Progress = &noopProgressing{}
	}

	fixedURL, err := rest.FixURI(url)
	if err != nil {
		return nil, err
	}
	g := &Client{
		client:             opt.Client,
		url:                fixedURL,
		fullPath:           opt.FullPath,
		contentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": fileName}),
		ID:                 generateSessionID(),
		chunkSize:          opt.ChunkSize,
		bearer:             bearerPrefix + opt.Bearer,
		progress:           opt.Progress,
		coroutines:         opt.Coroutines,
	}
	if err := g.init(); err != nil {
		return nil, err
	}

	return g, nil
}

// Init method initializes upload
func (c *Client) init() error {
	if c.fullPath != "" { // for upload
		return c.initUpload()
	}

	return c.initDownload()
}

func (c *Client) initDownload() error {
	r0, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("http.NewRequest %s: %w", c.url, err)
	}
	r0.Header.Set(Authorization, c.bearer)
	rr0, err := c.client.Do(r0)
	if err != nil {
		return err
	}
	contentRange := rr0.Header.Get(ContentRange)
	if rr0.StatusCode != http.StatusOK || contentRange == "" {
		return fmt.Errorf("no file to donwload or upload")
	}

	cr, err := parseContentRange(contentRange)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", contentRange, err)
	}
	_, params, err := mime.ParseMediaType(rr0.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}
	if err := ensureDir(ServerFileStorage.Path); err != nil {
		return err
	}

	c.fullPath = filepath.Join(ServerFileStorage.Path, params["filename"])
	c.TotalSize = cr.TotalSize
	c.wg.Add(1)

	go func() {
		defer c.wg.Done()

		log.Printf("Download %s started: %v", c.ID, c.fullPath)
		defer log.Printf("Download %s complete: %v", c.ID, c.fullPath)

		c.progress.Start(c.TotalSize)
		defer c.progress.Finish()
		if err := c.do("download", c.downloadChunk); err != nil {
			log.Printf("download error: %v", err)
		}
	}()

	return nil
}

func (c *Client) initUpload() error {
	fileStat, err := os.Stat(c.fullPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", c.fullPath, err)
	}

	c.TotalSize = uint64(fileStat.Size())
	c.wg.Add(1)

	go func() {
		defer c.wg.Done()

		log.Printf("Upload %s started: %v", c.ID, c.fullPath)
		defer log.Printf("Upload %s complete: %v", c.ID, c.fullPath)

		c.progress.Start(c.TotalSize)
		defer c.progress.Finish()

		if err := c.do("upload", c.uploadChunk); err != nil {
			log.Printf("upload error: %v", err)
		}
	}()

	return nil
}

func (c *Client) do(operation string, job func(i uint64) error) error {
	if c.coroutines <= 0 {
		for i := uint64(0); i < c.GetParts(); i++ {
			if err := job(i); err != nil {
				return err
			}
		}

		return nil
	}

	c.goJobs(operation, job)
	return nil
}

func (c *Client) downloadChunk(i uint64) error {
	partSize := GetPartSize(c.TotalSize, c.chunkSize, i)
	if partSize <= 0 {
		return nil
	}

	cr := newChunkRange(i, c.chunkSize, partSize, c.TotalSize)
	chunk, err := readChunk(c.fullPath, cr.From, cr.To)
	if err != nil {
		return fmt.Errorf("read %s: %w", c.fullPath, err)
	}

	r0, err := http.NewRequest(http.MethodGet, c.url, nil)
	r0.Header.Set(Authorization, c.bearer)
	r0.Header.Set(ContentRange, cr.createContentRange())
	r0.Header.Set(ContentDisposition, c.contentDisposition)
	r0.Header.Set(SessionID, c.ID)
	r0.Header.Set(ContentSha256, checksum(chunk))
	rr0, err := c.client.Do(r0)
	if err != nil {
		return err
	}
	defer Close(rr0.Body)

	c.progress.Add(partSize)
	if rr0.StatusCode == http.StatusNotModified {
		return nil
	}
	if rr0.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", rr0.StatusCode)
	}

	return writeChunk(c.fullPath, rr0.Body, cr)
}

func (c *Client) goJobs(operation string, job func(i uint64) error) {
	fnCh := make(chan uint64)
	var wg sync.WaitGroup
	for i := 0; i < c.coroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for idx := range fnCh {
				retryJob(func() error {
					err := job(idx)
					if err != nil {
						log.Printf("%s chunk %d: %v", operation, idx, err)
					}
					return err
				})
			}
		}()
	}

	for i := uint64(0); i < c.GetParts(); i++ {
		fnCh <- i
	}
	close(fnCh)

	wg.Wait()
}

func (c *Client) uploadChunk(i uint64) error {
	partSize := GetPartSize(c.TotalSize, c.chunkSize, i)
	if partSize <= 0 {
		return nil
	}

	cr := newChunkRange(i, c.chunkSize, partSize, c.TotalSize)
	chunk, err := readChunk(c.fullPath, cr.From, cr.To)
	if err != nil {
		return fmt.Errorf("read %s: %w", c.fullPath, err)
	}

	responseBody, err := c.chunkUpload(chunk, cr.createContentRange())
	if err != nil {
		return fmt.Errorf("chunk %d upload: %w", i+1, err)
	}

	c.progress.Add(partSize)

	_, err = parseBodyAsSizeTransferred(responseBody)
	if err != nil {
		return fmt.Errorf("parse body as size transferred %s: %w", responseBody, err)
	}

	return nil
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}

	return b
}

// Wait waits the upload complete
func (c *Client) Wait() {
	c.wg.Wait()
}

func (c *Client) chunkUpload(part []byte, contentRange string) (string, error) {
	r0, err := http.NewRequest(http.MethodGet, c.url, nil)

	r0.Header.Set(Authorization, c.bearer)
	r0.Header.Set(ContentRange, contentRange)
	r0.Header.Set(ContentDisposition, c.contentDisposition)
	r0.Header.Set(SessionID, c.ID)
	r0.Header.Set(ContentSha256, checksum(part))
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

	r, err := http.NewRequest(http.MethodPost, c.url, bytes.NewBuffer(part))
	if err != nil {
		return "", err
	}

	r.Header.Set(Authorization, c.bearer)
	r.Header.Set(ContentType, "application/octet-stream")
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set(ContentRange, contentRange)
	r.Header.Set(SessionID, c.ID)
	rr, err := c.client.Do(r)
	if err != nil {
		return "", err
	}
	defer Close(rr.Body)

	body, err := ioutil.ReadAll(rr.Body)
	if err != nil {
		return "", err
	}

	if rr.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %d", rr.StatusCode)
	}

	return string(body), nil
}
