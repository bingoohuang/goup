package goup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
)

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
	// ContentSha256 is the header name for Content-Sha256
	ContentSha256 = "Content-Sha256"
)

// Progressing is a progressing bar interface.
type Progressing interface {
	Start(value uint64)
	Add(value uint64)
	Finish()
}

type noopProgressing struct{}

func (n noopProgressing) Start(uint64) {}
func (n noopProgressing) Add(uint64)   {}
func (n noopProgressing) Finish()      {}

func writeChunk(fullPath string, chunk io.Reader, cr *chunkRange) error {
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_RDWR, 0o755)
	if err != nil {
		return fmt.Errorf("open file %s error: %w", fullPath, err)
	}
	defer Close(f)

	stat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file %s error: %w", fullPath, err)
	}
	if stat.Size() != int64(cr.TotalSize) {
		if err := f.Truncate(int64(cr.TotalSize)); err != nil {
			return fmt.Errorf("truncate file %s to size %d error: %w", fullPath, cr.TotalSize, err)
		}
	}

	if _, err := f.Seek(int64(cr.From), io.SeekStart); err != nil {
		return fmt.Errorf("seek file %s with pot %d error: %w", f.Name(), cr.From, err)
	}
	if _, err := io.Copy(f, chunk); err != nil {
		return fmt.Errorf("write file %s error: %w", fullPath, err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync file %s error: %w", fullPath, err)
	}

	return nil
}

func readChunk(fullPath string, partFrom, partTo uint64) ([]byte, error) {
	if fileNotExists(fullPath) {
		return nil, nil
	}

	f, err := os.OpenFile(fullPath, os.O_RDONLY, 0o755)
	if err != nil {
		return nil, fmt.Errorf("open file %s error: %w", fullPath, err)
	}
	defer Close(f)

	if _, err := f.Seek(int64(partFrom), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek file %s to %d error: %w", fullPath, partFrom, err)
	}
	chunk := make([]byte, partTo-partFrom)
	if n, err := f.Read(chunk); err != nil {
		return nil, fmt.Errorf("read file %s error: %w", fullPath, err)
	} else if n < int(partTo-partFrom) {
		return nil, fmt.Errorf("read file %s real %d < expected %d", fullPath, n, partTo-partFrom)
	}
	return chunk, nil
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

func checksum(part []byte) string {
	hash := sha256.New()
	hash.Write(part)
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func parseBodyAsSizeTransferred(body string) (uint64, error) {
	fromTo := strings.Split(body, "/")[0]
	split := strings.Split(fromTo, "-")
	return strconv.ParseUint(split[1], 10, 64)
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
