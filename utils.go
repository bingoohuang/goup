package goup

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func checkError(err error) {
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
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
	totalSize, err := strconv.ParseInt(strings.Split(contentRange, "/")[1], 10, 64)
	checkError(err)

	splitted := strings.Split(fromTo, "-")
	partFrom, err = strconv.ParseInt(splitted[0], 10, 64)
	checkError(err)
	partTo, err = strconv.ParseInt(splitted[1], 10, 64)
	checkError(err)

	return totalSize, partFrom, partTo
}

func parseBody(body string) uint64 {
	fromTo := strings.Split(body, "/")[0]
	splitted := strings.Split(fromTo, "-")

	partTo, err := strconv.ParseUint(splitted[1], 10, 64)
	checkError(err)

	return partTo
}

func fileExists(filePath string) bool {
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		return true
	}

	return false
}

func ensureDir(dirPath string) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		os.MkdirAll(dirPath, os.ModePerm)
	}
}
