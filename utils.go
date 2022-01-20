package goup

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

func checkError(format string, v ...interface{}) {
	if len(v) > 0 && v[len(v)-1] == nil {
		return
	}

	log.Fatalf(format, v...)
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

func parseBody(body string) uint64 {
	fromTo := strings.Split(body, "/")[0]
	split := strings.Split(fromTo, "-")
	partTo, err := strconv.ParseUint(split[1], 10, 64)
	checkError("parse int %s error: %v", split[1], err)
	return partTo
}

func fileNotExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return os.IsNotExist(err)
}

func ensureDir(dirPath string) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		err := os.MkdirAll(dirPath, os.ModePerm)
		checkError("mkdir %s error: %v", dirPath, err)
	}
}
