package goup

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"go.uber.org/multierr"

	"github.com/cespare/xxhash/v2"
)

// RootDir settings.
// When finished uploading with success files are stored inside it.
var RootDir = "./.goup"

const (
	// SessionID is the header name for Session-ID
	SessionID = "Session-ID"
	// Authorization is the header name for Authorization
	Authorization = "Authorization"
	// ContentRange is the header name for Content-Range
	ContentRange = "Content-Range"
	// ContentDisposition is header name for Content-Disposition
	ContentDisposition = "Content-Disposition"
	// ContentType is the header name for Content-Type
	ContentType = "Content-Type"
	// ContentChecksum is the header name for Content-Checksum
	ContentChecksum = "Content-Checksum"
	// ContentCurve is the header name for Content-Curve
	ContentCurve = "Content-Curve"
	// ContentSalt is the header name for Content-Salt
	ContentSalt = "Content-Salt"
	// ContentFilename is the header name for Content-Filename
	ContentFilename = "Content-Filename"
)

// Progress is a progress bar interface.
type Progress interface {
	Start(value uint64)
	Add(value uint64)
	Finish()
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

func writeChunk(fullPath string, chunk io.Reader, cr *chunkRange) (int64, error) {
	f, err := openChunk(fullPath, cr)
	if err != nil {
		return 0, err
	}

	defer Close(f)

	n, err := io.Copy(f, chunk)
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

func createChunkReader(fullPath string, partFrom, partTo uint64) (r io.ReadCloser, err error) {
	if fileNotExists(fullPath) {
		return nil, nil
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

	if _, err := f.Seek(int64(partFrom), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek file %s to %d error: %w", fullPath, partFrom, err)
	}

	reader := io.LimitReader(f, int64(partTo-partFrom))
	return Wrap(reader, f), nil
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
		log.Printf("copy error: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
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
		log.Printf("close error: %v", err)
	}
}
