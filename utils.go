package goup

import (
	"crypto/rand"
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

func parseContentRange(contentRange string) (totalSize int64, partFrom int64, partTo int64) {
	contentRange = strings.Replace(contentRange, "bytes ", "", -1)
	fromTo := strings.Split(contentRange, "/")[0]
	totalSizeStr := strings.Split(contentRange, "/")[1]
	totalSize, err := strconv.ParseInt(totalSizeStr, 10, 64)
	checkError("parse int %s error: %v", totalSizeStr, err)

	splitted := strings.Split(fromTo, "-")
	partFrom, err = strconv.ParseInt(splitted[0], 10, 64)
	checkError("parse int %s error: %v", splitted[0], err)
	partTo, err = strconv.ParseInt(splitted[1], 10, 64)
	checkError("parse int %s error: %v", splitted[1], err)

	return totalSize, partFrom, partTo
}

func parseBody(body string) uint64 {
	fromTo := strings.Split(body, "/")[0]
	split := strings.Split(fromTo, "-")
	partTo, err := strconv.ParseUint(split[1], 10, 64)
	checkError("parse int %s error: %v", split[1], err)
	return partTo
}

func fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return !os.IsNotExist(err)
}

func ensureDir(dirPath string) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		err := os.MkdirAll(dirPath, os.ModePerm)
		checkError("mkdir %s error: %v", dirPath, err)
	}
}
