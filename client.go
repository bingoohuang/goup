package goup

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bingoohuang/goup/shapeio"

	"github.com/bingoohuang/gg/pkg/ss"

	"github.com/bingoohuang/goup/codec"
	"github.com/minio/sio"

	"github.com/schollz/pake/v3"

	"github.com/bingoohuang/gg/pkg/rest"
)

// Client structure
type Client struct {
	*Opt
	url                string
	ID                 string
	TotalSize          uint64
	wg                 sync.WaitGroup
	contentDisposition string
	sessionKey         []byte
	LimitRate          uint64
}

// GetParts get the number of chunk parts.
func (c *Client) GetParts() uint64 {
	return uint64(math.Ceil(float64(c.TotalSize) / float64(c.ChunkSize)))
}

// Opt is the client options.
type Opt struct {
	ChunkSize uint64
	LimitRate uint64
	Progress
	*http.Client
	Rename     string
	Bearer     string
	FullPath   string
	Code       string
	Coroutines int
	Cipher     string
}

// OptFn is the option pattern func prototype.
type OptFn func(*Opt)

// WithHTTPClient set *http.Client.
func WithHTTPClient(v *http.Client) OptFn { return func(c *Opt) { c.Client = v } }

// WithChunkSize set ChunkSize.
func WithChunkSize(v uint64) OptFn { return func(c *Opt) { c.ChunkSize = v } }

// WithLimitRate set LimitRate.
func WithLimitRate(v uint64) OptFn { return func(c *Opt) { c.LimitRate = v } }

// WithProgress set WithProgress.
func WithProgress(v Progress) OptFn { return func(c *Opt) { c.Progress = v } }

// WithRename set WithRename.
func WithRename(v string) OptFn { return func(c *Opt) { c.Rename = v } }

// WithBearer set Bearer.
func WithBearer(v string) OptFn { return func(c *Opt) { c.Bearer = v } }

// WithFullPath set FullPath.
func WithFullPath(v string) OptFn { return func(c *Opt) { c.FullPath = v } }

// WithCipher set cipher.
func WithCipher(v string) OptFn { return func(c *Opt) { c.Cipher = v } }

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
	if !strings.HasPrefix(opt.Bearer, bearerPrefix) {
		opt.Bearer = bearerPrefix + opt.Bearer
	}

	fixedURL := rest.FixURI(url)
	if !fixedURL.OK() {
		return nil, fixedURL.Err
	}
	g := &Client{
		Opt:                opt,
		url:                fixedURL.Data.String(),
		contentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": fileName}),
		ID:                 generateSessionID(),
	}

	return g, nil
}

// Start method initializes upload
func (c *Client) Start() (err error) {
	if c.ChunkSize > 0 {
		if err := c.setupSessionKey(); err != nil {
			return err
		}
	}

	if c.FullPath != "" { // for upload
		return c.initUpload()
	}

	if err := ensureDir(RootDir); err != nil {
		return err
	}

	if c.ChunkSize > 0 {
		return c.initDownload()
	}

	return c.multipartDownload()
}

func (c *Client) multipartDownload() error {
	r, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("http.NewRequest %s: %w", c.url, err)
	}
	r.Header.Set(Authorization, c.Bearer)
	q, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	_, params, err := mime.ParseMediaType(q.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}
	c.FullPath = filepath.Join(RootDir, params["filename"])
	if length := ss.ParseUint64(q.Header.Get("Content-Length")); length > 0 {
		c.TotalSize = length
	}

	if c.LimitRate > 0 {
		q.Body = shapeio.NewReader(q.Body, shapeio.WithRateLimit(float64(c.LimitRate)))
	}

	log.Printf("Download %s started: %v", c.ID, c.FullPath)
	defer log.Printf("Download %s complete: %v", c.ID, c.FullPath)

	c.Progress.Start(c.TotalSize)
	defer c.Progress.Finish()
	if _, err := writeChunk(c.FullPath, c.Progress, q.Body, nil); err != nil {
		log.Printf("write chunk error: %v", err)
	}

	return nil
}

func (c *Client) initDownload() error {
	r, err := http.NewRequest(http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("http.NewRequest %s: %w", c.url, err)
	}
	r.Header.Set("Content-Gulp", "Session="+c.ID)
	r.Header.Set(Authorization, c.Bearer)
	q, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	h := ParseHeader(q.Header.Get("Content-Gulp"))
	if q.StatusCode != http.StatusOK || h.Range == "" {
		return fmt.Errorf("no file to donwload or upload")
	}

	cr, err := parseContentRange(h.Range)
	if err != nil {
		return fmt.Errorf("parse contentRange %s error: %w", h.Range, err)
	}

	_, params, err := mime.ParseMediaType(q.Header.Get(ContentDisposition))
	if err != nil {
		return fmt.Errorf("parse Content-Disposition error: %w", err)
	}
	c.FullPath = filepath.Join(RootDir, params["filename"])
	c.TotalSize = cr.TotalSize

	log.Printf("Download %s started: %v", c.ID, c.FullPath)
	defer log.Printf("Download %s complete: %v", c.ID, c.FullPath)

	c.Progress.Start(c.TotalSize)
	defer c.Progress.Finish()
	if err := c.do("download", c.downloadChunk); err != nil {
		log.Printf("download error: %v", err)
	}

	return nil
}

func (c *Client) initUpload() error {
	fileStat, err := os.Stat(c.FullPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", c.FullPath, err)
	}

	c.TotalSize = uint64(fileStat.Size())

	log.Printf("Upload %s started: %v", c.ID, c.FullPath)
	defer log.Printf("Upload %s complete: %v", c.ID, c.FullPath)

	c.Progress.Start(c.TotalSize)
	defer c.Progress.Finish()

	if err := func() error {
		if c.ChunkSize == 0 {
			return c.uploadMultipartForm()
		}
		return c.do("upload", c.uploadChunk)
	}(); err != nil {
		log.Printf("upload error: %v", err)
		return err
	}

	return nil
}

func (c *Client) do(operation string, job func(i uint64) error) error {
	if c.Coroutines <= 0 {
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
	partSize := GetPartSize(c.TotalSize, c.ChunkSize, i)
	if partSize <= 0 {
		return nil
	}

	cr := newChunkRange(i, c.ChunkSize, partSize, c.TotalSize)
	chunkChecksum, err := readChunkChecksum(c.FullPath, cr.From, cr.To)
	if err != nil {
		return fmt.Errorf("read %s: %w", c.FullPath, err)
	}

	r, err := http.NewRequest(http.MethodGet, c.url, nil)
	r.Header.Set(Authorization, c.Bearer)
	r.Header.Set("Content-Gulp", "Session="+c.ID+
		"; Range="+cr.createContentRange()+
		"; Checksum="+chunkChecksum)
	r.Header.Set(ContentDisposition, c.contentDisposition)
	q, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	defer Close(q.Body)

	if q.StatusCode == http.StatusNotModified {
		c.Progress.Add(partSize)
		return nil
	}
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	h := ParseHeader(q.Header.Get("Content-Gulp"))
	salt, err := base64.RawURLEncoding.DecodeString(h.Salt)
	if err != nil {
		return err
	}

	if q.Body == nil {
		return fmt.Errorf("response body is nil")
	}

	if c.LimitRate > 0 {
		q.Body = shapeio.NewReader(q.Body, shapeio.WithRateLimit(float64(c.LimitRate)))
	}

	key, _, err := codec.Scrypt(c.sessionKey, salt)
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	go func() {
		defer Close(pw)

		_, cipherSuites := parseCipherSuites(c.Cipher)
		cfg := sio.Config{Key: key, CipherSuites: cipherSuites}
		if n, err := sio.Decrypt(pw, q.Body, cfg); err != nil {
			log.Printf("decrypt bytes: %d error: %v", n, err)
		}
	}()

	if _, err := writeChunk(c.FullPath, c.Progress, pr, cr); err != nil {
		return fmt.Errorf("write chunk error: %w", err)
	}
	return nil
}

func (c *Client) goJobs(operation string, job func(i uint64) error) {
	fnCh := make(chan uint64)
	var wg sync.WaitGroup
	for i := 0; i < c.Coroutines; i++ {
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

func (c *Client) uploadMultipartForm() error {
	fileReader, err := CreateChunkReader(c.FullPath, 0, 0, c.LimitRate)
	if err != nil {
		return err
	}
	defer Close(fileReader)

	up := PrepareMultipartPayload(map[string]interface{}{
		"file": &PbReader{Reader: fileReader, Adder: c.Progress},
	})
	r, err := http.NewRequest(http.MethodPost, c.url, up.Body)
	if err != nil {
		return err
	}
	for k, v := range up.Headers {
		r.Header.Set(k, v)
	}
	r.ContentLength = up.Size
	r.Header.Set("Content-Gulp", "Session:"+c.ID)
	r.Header.Set(Authorization, c.Bearer)
	q, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	defer Close(q.Body)

	_, _ = io.Copy(io.Discard, q.Body)
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	return nil
}

func (c *Client) uploadChunk(i uint64) error {
	partSize := GetPartSize(c.TotalSize, c.ChunkSize, i)
	if partSize <= 0 {
		return nil
	}

	cr := newChunkRange(i, c.ChunkSize, partSize, c.TotalSize)

	chunkChecksum, err := readChunkChecksum(c.FullPath, cr.From, cr.To)
	if err != nil {
		return fmt.Errorf("readChunkChecksum %s: %w", c.FullPath, err)
	}
	r, err := CreateChunkReader(c.FullPath, cr.From, cr.To, c.LimitRate)
	if err != nil {
		return fmt.Errorf("CreateChunkReader %s: %w", c.FullPath, err)
	}
	defer Close(r)

	responseBody, err := c.chunkUpload(r, cr, chunkChecksum)
	if err != nil {
		return fmt.Errorf("chunk %d upload: %w", i+1, err)
	}

	if _, err := parseContentRange(responseBody); err != nil {
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
	a, err := pake.InitCurve([]byte(c.Code), 0, "siec")
	if err != nil {
		return fmt.Errorf("init curve failed: %w", err)
	}
	r, err := http.NewRequest(http.MethodPost, c.url, nil)
	if err != nil {
		return err
	}
	r.Header.Set(Authorization, c.Bearer)
	r.Header.Set("Content-Gulp", "Session="+c.ID+
		"; Curve="+base64.RawURLEncoding.EncodeToString(a.Bytes()))
	q, err := c.Client.Do(r)
	if err != nil {
		return err
	}
	if q.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code: %d", q.StatusCode)
	}

	h := ParseHeader(q.Header.Get("Content-Gulp"))
	b, err := base64.RawURLEncoding.DecodeString(h.Curve)
	if err != nil {
		return fmt.Errorf("base64 decode error: %w", err)
	} else if err := a.Update(b); err != nil {
		return fmt.Errorf("update b error: %w", err)
	}

	ak, err := a.SessionKey()
	if err != nil {
		return err
	}
	c.sessionKey = ak
	return nil
}

func (c *Client) chunkUpload(part io.ReadCloser, cr *chunkRange, chunkChecksum string) (string, error) {
	contentRange := cr.createContentRange()
	notModified, err := c.chunkUploadChecksum(chunkChecksum, contentRange)
	if err != nil {
		return "", err
	}
	if notModified {
		c.Progress.Add(cr.PartSize)
		return contentRange, nil
	}

	return c.chunkTransfer(part, contentRange, err)
}

func (c *Client) chunkTransfer(chunkBody io.Reader, contentRange string, err error) (string, error) {
	salt := codec.GenSalt(8)
	key, _, err := codec.Scrypt(c.sessionKey, salt)
	if err != nil {
		return "", err
	}

	pr, pw := io.Pipe()
	go func() {
		defer Close(pw)

		_, cipherSuites := parseCipherSuites(c.Cipher)
		cfg := sio.Config{Key: key, CipherSuites: cipherSuites}
		if n, err := sio.Encrypt(pw, chunkBody, cfg); err != nil {
			log.Printf("encrypt data bytes: %d, error: %v", n, err)
		}
	}()

	r, err := http.NewRequest(http.MethodPost, c.url, &PbReader{Reader: pr, Adder: c.Progress})
	if err != nil {
		return "", err
	}

	r.Header.Set(Authorization, c.Bearer)
	r.Header.Set(ContentType, "application/octet-stream")
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set("Content-Gulp", "Session="+c.ID+
		"; Range="+contentRange+
		"; Salt="+base64.RawURLEncoding.EncodeToString(salt))
	q, err := c.Client.Do(r)
	if err != nil {
		return "", err
	}
	defer Close(q.Body)

	body, err := io.ReadAll(q.Body)
	if err != nil {
		return "", err
	}

	if q.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status code: %d, body: %s", q.StatusCode, body)
	}

	return string(body), nil
}

func (c *Client) chunkUploadChecksum(chunkChecksum, contentRange string) (bool, error) {
	r, err := http.NewRequest(http.MethodGet, c.url, nil)

	r.Header.Set(Authorization, c.Bearer)
	r.Header.Set(ContentDisposition, c.contentDisposition)
	r.Header.Set("Content-Gulp", "Session="+c.ID+
		"; Range="+contentRange+
		"; Checksum="+chunkChecksum)
	q, err := c.Client.Do(r)
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
