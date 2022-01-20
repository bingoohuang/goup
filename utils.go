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

// GetPartSize get the part size of idx-th chunk.
func GetPartSize(totalSize, chunkSize, idx uint64) uint64 {
	return min(chunkSize, totalSize-idx*chunkSize)
}

func generateSessionID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%X", b)
}

func generateContentRange(index, fileChunk, partSize, totalSize uint64) string {
	from := fileChunk * index
	return fmt.Sprintf("bytes %d-%d/%d", from, from+partSize, totalSize)
}

func parseContentRange(contentRange string) (totalSize, partFrom, partTo int64, err error) {
	contentRange = strings.Replace(contentRange, "bytes ", "", -1)
	fromTo := strings.Split(contentRange, "/")[0]
	totalSizeStr := strings.Split(contentRange, "/")[1]
	totalSize, err = strconv.ParseInt(totalSizeStr, 10, 64)
	if err != nil {
		return
	}

	splitted := strings.Split(fromTo, "-")
	partFrom, err = strconv.ParseInt(splitted[0], 10, 64)
	if err != nil {
		return
	}

	partTo, err = strconv.ParseInt(splitted[1], 10, 64)
	return
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
