package goup

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/schollz/pake/v3"

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
	code               string
	progress           Progress
	coroutines         int
	sessionKey         []byte
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
	Code       string
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

// WithCode set Code.
func WithCode(v string) OptFn { return func(c *Opt) { c.Code = v } }

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
		code:               opt.Code,
	}
	if err := g.init(); err != nil {
		return nil, err
	}

	return g, nil
}

// Init method initializes upload
func (c *Client) init() (err error) {
	if err := c.setupSessionKey(); err != nil {
		return err
	}

	if c.fullPath != "" { // for upload
		return c.initUpload()
	}

	return c.initDownload()
}

func (c *Client) initDownload() error {
	r, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("http.NewRequest %s: %w", c.url, err)
	}
	r.Header.Set(SessionID, c.ID)
	r.Header.Set(Authorization, c.bearer)
	q, err := c.client.Do(r)
	if err != nil {
		return err
	}
	contentRange := q.Header.Get(ContentRange)
	if q.StatusCode != http.StatusOK || contentRange == "" {
		return fmt.Errorf("no file to donwload or upload")
	}

	cr, err := parseContentRange(contentRange)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", contentRange, err)
	}

	if err := ensureDir(RootDir); err != nil {
		return err
	}

	_, params, err := mime.ParseMediaType(q.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}
	c.fullPath = filepath.Join(RootDir, params["filename"])
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

	r, err := http.NewRequest(http.MethodGet, c.url, nil)
	r.Header.Set(SessionID, c.ID)
	r.Header.Set(Authorization, c.bearer)
	r.Header.Set(ContentRange, cr.createContentRange())
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set(ContentSha256, checksum(chunk))
	q, err := c.client.Do(r)
	if err != nil {
		return err
	}
	defer Close(q.Body)

	c.progress.Add(partSize)
	if q.StatusCode == http.StatusNotModified {
		return nil
	}
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	var body bytes.Buffer
	if _, err := io.Copy(&body, q.Body); err != nil {
		return err
	}

	data, err := Decrypt(body.Bytes(), c.sessionKey)
	if err != nil {
		return fmt.Errorf("failed to decrypt: %w", err)
	}

	return writeChunk(c.fullPath, bytes.NewReader(data), cr)
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

func (c *Client) setupSessionKey() error {
	a, err := pake.InitCurve([]byte(c.code), 0, "siec")
	if err != nil {
		return fmt.Errorf("init curve failed: %w", err)
	}
	r, err := http.NewRequest(http.MethodPost, c.url, nil)
	if err != nil {
		return err
	}
	r.Header.Set(SessionID, c.ID)
	r.Header.Set(Authorization, c.bearer)
	salt := genSalt()
	base64fn := base64.RawURLEncoding.EncodeToString
	baseA := base64fn(a.Bytes())
	baseSalt := base64fn(salt)
	r.Header.Set(ContentCurve, baseA+"/"+baseSalt)
	q, err := c.client.Do(r)
	if err != nil {
		return err
	}
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	cc := q.Header.Get(ContentCurve)
	b, err := base64.RawURLEncoding.DecodeString(cc)
	if err != nil {
		return fmt.Errorf("base64 decode error: %w", err)
	} else if err := a.Update(b); err != nil {
		return fmt.Errorf("update b error: %w", err)
	}

	ak, err := a.SessionKey()
	if err != nil {
		return err
	}
	if c.sessionKey, _, err = NewKey(ak, salt); err != nil {
		return fmt.Errorf("new key error: %w", err)
	}

	return nil
}

func (c *Client) chunkUpload(part []byte, contentRange string) (string, error) {
	notModified, err := c.chunkUploadChecksum(part, contentRange)
	if err != nil {
		return "", err
	}
	if notModified {
		return contentRange, nil
	}

	return c.chunkTransfer(part, contentRange, err)
}

func (c *Client) chunkTransfer(part []byte, contentRange string, err error) (string, error) {
	data, err := Encrypt(part, c.sessionKey)
	if err != nil {
		return "", fmt.Errorf("encrypt data error: %w", err)
	}

	r, err := http.NewRequest(http.MethodPost, c.url, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}

	r.Header.Set(SessionID, c.ID)
	r.Header.Set(Authorization, c.bearer)
	r.Header.Set(ContentType, "application/octet-stream")
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set(ContentRange, contentRange)
	q, err := c.client.Do(r)
	if err != nil {
		return "", err
	}
	defer Close(q.Body)

	body, err := ioutil.ReadAll(q.Body)
	if err != nil {
		return "", err
	}

	if q.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	return string(body), nil
}

func (c *Client) chunkUploadChecksum(part []byte, contentRange string) (bool, error) {
	r, err := http.NewRequest(http.MethodGet, c.url, nil)

	r.Header.Set(SessionID, c.ID)
	r.Header.Set(Authorization, c.bearer)
	r.Header.Set(ContentRange, contentRange)
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set(ContentSha256, checksum(part))
	q, err := c.client.Do(r)
	if err != nil {
		return false, err
	}
	if q.StatusCode == http.StatusNotModified {
		return true, nil
	}

	if q.StatusCode != 200 {
		return false, fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	return false, nil
}
