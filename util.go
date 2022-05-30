package goup

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bingoohuang/gg/pkg/codec/b64"

	"github.com/bingoohuang/goup/shapeio"

	"go.uber.org/multierr"

	"github.com/cespare/xxhash/v2"
)

// RootDir settings.
// When finished uploading with success files are stored inside it.
var RootDir = "./.goup"

const (
	// Authorization is the header name for Authorization
	Authorization = "Authorization"
	// ContentDisposition is header name for Content-Disposition
	ContentDisposition = "Content-Disposition"
	// ContentType is the header name for Content-Type
	ContentType = "Content-Type"
	// ContentLength is the header name for Content-Length
	ContentLength = "Content-Length"
)

// Header is a header structure for the goup file transfer.
type Header struct {
	Session  string
	Checksum string
	Curve    string
	Salt     string
	Range    string
	Filename string
}

// ParseHeader parse the Content-Gulp Header to structure.
func ParseHeader(header string) Header {
	m := map[string]string{}
	items := strings.Split(header, ";")
	for _, item := range items {
		pos := strings.Index(item, "=")
		var k, v string
		if pos < 0 {
			k = item
			v = ""
		} else if pos >= 0 {
			k = item[:pos]
			v = item[pos+1:]
		}
		k, _ = url.QueryUnescape(k)
		v, _ = url.QueryUnescape(v)
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" {
			m[textproto.CanonicalMIMEHeaderKey(k)] = v
		}
	}

	return Header{
		Session:  m["Session"],
		Checksum: m["Checksum"],
		Curve:    m["Curve"],
		Salt:     m["Salt"],
		Range:    m["Range"],
		Filename: m["Filename"],
	}
}

// Progress is a progress bar interface.
type Progress interface {
	Start(value uint64)
	Add(value uint64)
	Finish()
}

// Adder defines a addable interface.
type Adder interface {
	Add(value uint64)
}

// AdderFn is a func prototype which implements Adder interface.
type AdderFn func(value uint64)

// Add adds a value.
func (f AdderFn) Add(value uint64) {
	f(value)
}

type noopProgressing struct{}

func (n noopProgressing) Start(uint64) {}
func (n noopProgressing) Add(uint64)   {}
func (n noopProgressing) Finish()      {}

func openChunk(fullPath string, cr *chunkRange) (f *os.File, err error) {
	f, err = os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		return f, fmt.Errorf("open file %s error: %w", fullPath, err)
	}
	defer func() {
		if err != nil && f != nil {
			Close(f)
			f = nil
		}
	}()

	if cr != nil {
		stat, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("stat file %s error: %w", fullPath, err)
		}
		if stat.Size() != int64(cr.TotalSize) {
			if err := f.Truncate(int64(cr.TotalSize)); err != nil {
				return nil, fmt.Errorf("truncate file %s to size %d error: %w", fullPath, cr.TotalSize, err)
			}
		}
		if _, err := f.Seek(int64(cr.From), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek file %s with pot %d error: %w", f.Name(), cr.From, err)
		}
	}

	return f, nil
}

func writeChunk(fullPath string, progress Progress, chunk io.Reader, cr *chunkRange) (int64, error) {
	f, err := openChunk(fullPath, cr)
	if err != nil {
		return 0, err
	}

	defer Close(f)

	n, err := io.Copy(f, &PbReader{Reader: chunk, Adder: progress})
	if err != nil {
		return 0, fmt.Errorf("write file %s error: %w", fullPath, err)
	}

	return n, nil
}

type wrapReader struct {
	reader io.Reader
	closer io.Closer
}

// Wrap wraps reader and closer together to create an new io.ReadCloser.
//
// The Read function will simply call the wrapped reader's Read function,
// while the Close function will call the wrapped closer's Close function.
//
// If the wrapped reader is also an io.Closer,
// its Close function will be called in Close as well.
func Wrap(reader io.Reader, closer io.Closer) io.ReadCloser {
	return &wrapReader{reader: reader, closer: closer}
}

func (r *wrapReader) Read(p []byte) (int, error) { return r.reader.Read(p) }

func (r *wrapReader) Close() (err error) {
	if closer, ok := r.reader.(io.Closer); ok {
		err = closer.Close()
	}
	if r.closer != nil {
		err = multierr.Append(err, r.closer.Close())
	}

	return
}

func readChunkChecksum(fullPath string, partFrom, partTo uint64) (checksum string, err error) {
	if fileNotExists(fullPath) {
		return "", nil
	}

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0o755)
	if err != nil {
		return "", fmt.Errorf("open file %s error: %w", fullPath, err)
	}
	defer Close(f)

	if _, err := f.Seek(int64(partFrom), io.SeekStart); err != nil {
		return "", fmt.Errorf("seek file %s to %d error: %w", fullPath, partFrom, err)
	}

	reader := io.LimitReader(f, int64(partTo-partFrom))
	checksum = checksumReader(reader)

	return checksum, nil
}

// Rewindable is an interface for anything that can be rewind like a file reader to seek start.
type Rewindable interface {
	Rewind() error
}

// RewindableFn is a func which implements Rewindable.
type RewindableFn func() error

// Rewind do rewind.
func (f RewindableFn) Rewind() error {
	return f()
}

type readCloseRewindable struct {
	io.ReadCloser
	PayloadFileReader
	Rewindable
}

// CreateChunkReader creates a chunk reader for the file.
func CreateChunkReader(fullPath string, partFrom, partTo uint64, limitRate uint64) (r io.ReadCloser, err error) {
	if fileNotExists(fullPath) {
		return nil, fmt.Errorf("file %s not exists", fullPath)
	}

	defer func() {
		if err != nil && r != nil {
			Close(r)
			r = nil
		}
	}()

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0o755)
	if err != nil {
		return nil, fmt.Errorf("open file %s error: %w", fullPath, err)
	}

	if partFrom > 0 {
		if _, err := f.Seek(int64(partFrom), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek file %s to %d error: %w", fullPath, partFrom, err)
		}

		reader := io.LimitReader(f, int64(partTo-partFrom))
		return Wrap(reader, f), nil
	}

	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}

	pf := &PayloadFile{ReadCloser: f, Name: f.Name(), Size: stat.Size()}

	if limitRate > 0 {
		pf.ReadCloser = shapeio.NewReader(pf.ReadCloser, shapeio.WithRateLimit(float64(limitRate)))
	}

	rcr := &readCloseRewindable{
		ReadCloser:        pf,
		PayloadFileReader: pf,
		Rewindable: RewindableFn(func() error {
			_, err := f.Seek(0, io.SeekStart)
			return err
		}),
	}

	return rcr, nil
}

// GetPartSize get the part size of idx-th chunk.
func GetPartSize(totalSize, chunkSize, idx uint64) uint64 {
	return min(chunkSize, totalSize-idx*chunkSize)
}

func generateSessionID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%X", b)
}

type chunkRange struct {
	From uint64
	To   uint64
	PartSize,
	TotalSize uint64
}

func newChunkRange(index, fileChunk, partSize, totalSize uint64) *chunkRange {
	return &chunkRange{
		From:      fileChunk * index,
		To:        fileChunk*index + partSize,
		PartSize:  partSize,
		TotalSize: totalSize,
	}
}

func (c chunkRange) createContentRange() string {
	return fmt.Sprintf("bytes %d-%d/%d", c.From, c.To, c.TotalSize)
}

func parseContentRange(contentRange string) (c *chunkRange, err error) {
	contentRange = strings.Replace(contentRange, "bytes ", "", -1)
	fromTo := strings.Split(contentRange, "/")[0]
	totalSizeStr := strings.Split(contentRange, "/")[1]
	totalSize, err := strconv.ParseUint(totalSizeStr, 10, 64)
	if err != nil {
		return nil, err
	}

	split := strings.Split(fromTo, "-")
	partFrom, err := strconv.ParseUint(split[0], 10, 64)
	if err != nil {
		return nil, err
	}

	partTo, err := strconv.ParseUint(split[1], 10, 64)
	if err != nil {
		return nil, err
	}

	return &chunkRange{
		From:      partFrom,
		To:        partTo,
		PartSize:  partTo - partFrom,
		TotalSize: totalSize,
	}, nil
}

func checksumReader(r io.Reader) string {
	h := xxhash.New()
	if _, err := io.Copy(h, r); err != nil {
		log.Printf("copy failed: %v", err)
	}
	return b64.EncodeBytes2String(h.Sum(nil), b64.Raw, b64.URL)
}

func fileNotExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return os.IsNotExist(err)
}

func ensureDir(dirPath string) error {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return os.MkdirAll(dirPath, os.ModePerm)
	}
	return nil
}

// Close closes the io.Closer and log print if error occurs.
func Close(c io.Closer) {
	if err := c.Close(); err != nil {
		log.Printf("close failed: %v", err)
	}
}

// PbReader is a wrapper reader for Adder.
type PbReader struct {
	io.Reader
	Adder
}

func (r *PbReader) Read(p []byte) (n int, err error) {
	n, err = r.Reader.Read(p)
	if r.Adder != nil {
		r.Adder.Add(uint64(n))
	}
	return
}

// MultipartPayload is the multipart payload.
type MultipartPayload struct {
	Headers map[string]string
	Body    io.Reader
	Size    int64
}

// PayloadFileReader is the interface which means a reader which represents a file.
type PayloadFileReader interface {
	FileName() string
	FileSize() int64
}

// PayloadFile means the file payload.
type PayloadFile struct {
	io.ReadCloser

	Name string
	Size int64
}

// FileName returns the filename
func (p PayloadFile) FileName() string { return p.Name }

// FileSize returns the file size.
func (p PayloadFile) FileSize() int64 { return p.Size }

const (
	crlf = "\r\n"
)

// Rewind rewinds the io.Reader.
func Rewind(reader io.Reader) (err error) {
	if r1, ok1 := reader.(io.Seeker); ok1 {
		if _, e0 := r1.Seek(0, io.SeekStart); e0 != nil {
			err = multierr.Append(err, e0)
		}
	} else if r2, ok2 := reader.(Rewindable); ok2 {
		if e0 := r2.Rewind(); e0 != nil {
			err = multierr.Append(err, e0)
		}
	}

	return
}

// PrepareMultipartPayload prepares the multipart playload of http request.
// Multipart request has the following structure:
//  POST /upload HTTP/1.1
//  Other-Headers: ...
//  Content-Type: multipart/form-data; boundary=$boundary
//  \r\n
//  --$boundary\r\n    👈 request body starts here
//  Content-Disposition: form-data; name="field1"\r\n
//  Content-Type: text/plain; charset=utf-8\r\n
//  Content-Length: 4\r\n
//  \r\n
//  $content\r\n
//  --$boundary\r\n
//  Content-Disposition: form-data; name="field2"\r\n
//  ...
//  --$boundary--\r\n
// https://stackoverflow.com/questions/39761910/how-can-you-upload-files-as-a-stream-in-go/39781706
// https://blog.depa.do/post/bufferless-multipart-post-in-go
// https://github.com/technoweenie/multipartstreamer
func PrepareMultipartPayload(fields map[string]interface{}) *MultipartPayload {
	var buf [8]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		panic(err)
	}
	boundary := fmt.Sprintf("%x", buf[:])
	totalSize := 0
	headers := map[string]string{
		"Content-Type": fmt.Sprintf("multipart/form-data; boundary=%s", boundary),
	}

	parts := make([]io.Reader, 0)

	fieldBoundary := "--" + boundary + crlf
	str := strings.NewReader

	for k, v := range fields {
		if v == nil {
			continue
		}

		parts = append(parts, str(fieldBoundary))
		totalSize += len(fieldBoundary)

		switch vf := v.(type) {
		case string:
			header := fmt.Sprintf(`Content-Disposition: form-data; name="%s"`, k)
			parts = append(parts, str(header+crlf+crlf), str(v.(string)), str(crlf))
			totalSize += len(header) + len(crlf+crlf) + len(v.(string)) + len(crlf)
		case io.Reader:
			if pf, ok := vf.(PayloadFileReader); ok {
				fileName := pf.FileName()
				header := strings.Join([]string{
					fmt.Sprintf(`Content-Disposition: form-data; name="%s"; filename="%s"`, k, filepath.Base(fileName)),
					fmt.Sprintf(`Content-Type: %s`, mime.TypeByExtension(filepath.Ext(fileName))),
					fmt.Sprintf(`Content-Length: %d`, pf.FileSize()),
				}, crlf)
				parts = append(parts, str(header+crlf+crlf), vf, str(crlf))
				totalSize += len(header) + len(crlf+crlf) + int(pf.FileSize()) + len(crlf)
			} else {
				log.Printf("Ignore unsupported multipart payload type %t", v)
			}
		default:
			log.Printf("Ignore unsupported multipart payload type %t", v)
		}
	}

	finishBoundary := "--" + boundary + "--" + crlf
	parts = append(parts, str(finishBoundary))
	totalSize += len(finishBoundary)
	headers["Content-Length"] = fmt.Sprintf("%d", totalSize)

	return &MultipartPayload{Headers: headers, Body: NewRewindableMultiReader(parts...), Size: int64(totalSize)}
}

// RewindableMultiReader implements a rewindable multi-reader.
type RewindableMultiReader struct {
	readers     []io.Reader
	readerIndex int
}

func (mr *RewindableMultiReader) Read(p []byte) (n int, err error) {
	for mr.readerIndex < len(mr.readers) {
		reader := mr.readers[mr.readerIndex]
		n, err = reader.Read(p)
		if err != nil && errors.Is(err, io.EOF) {
			if err0 := Rewind(reader); err0 != nil {
				return n, err0
			}

			mr.readerIndex++
			err = nil
			continue
		}
		if n > 0 {
			return
		}
	}

	mr.readerIndex = 0
	return 0, io.EOF
}

// NewRewindableMultiReader returns a Reader that's the logical concatenation of
// the provided input readers. They're read sequentially. Once all
// inputs have returned EOF, Read will return EOF.  If any of the readers
// return a non-nil, non-EOF error, Read will return that error.
func NewRewindableMultiReader(readers ...io.Reader) io.Reader {
	r := make([]io.Reader, len(readers))
	copy(r, readers)
	return &RewindableMultiReader{readers: r}
}
