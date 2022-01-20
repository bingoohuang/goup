package goup

import (
	"io/ioutil"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type uploadFile struct {
	file        *os.File
	name        string
	tempPath    string
	status      time.Time
	size        int64
	transferred int64
	start       time.Time
}

var files = make(map[string]*uploadFile)
var filesLock sync.Mutex

func deleteUploadFile(sessionID string) {
	filesLock.Lock()
	defer filesLock.Unlock()

	delete(files, sessionID)
}
func saveUploadFile(sessionID string, f *uploadFile) {
	filesLock.Lock()
	defer filesLock.Unlock()

	files[sessionID] = f
}

func getUploadFile(sessionID string) (*uploadFile, bool) {
	filesLock.Lock()
	defer filesLock.Unlock()

	f, ok := files[sessionID]
	return f, ok
}

type fileStorage struct {
	Path     string
	TempPath string
}

// FileStorage settings.
// When finished uploading with success files are stored inside Path config.
// While uploading temporary files are stored inside TempPath directory.
var FileStorage = fileStorage{
	Path:     "./files",
	TempPath: ".tmp",
}

// HTTPHandler is main request/response handler for HTTP server.
func HTTPHandler(w http.ResponseWriter, r *http.Request) {
	ensureDir(FileStorage.Path)
	ensureDir(FileStorage.TempPath)

	header := r.Header.Get
	sessionID := header("Session-ID")
	contentRange := header("Content-Range")
	if r.Method != "POST" || sessionID == "" || contentRange == "" {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Invalid request."))
		return
	}

	body, err := ioutil.ReadAll(r.Body)
	checkError(err)

	totalSize, partFrom, partTo := parseContentRange(contentRange)
	u, _ := getUploadFile(sessionID)
	if partFrom == 0 {
		w.WriteHeader(http.StatusCreated)

		_, params, err := mime.ParseMediaType(header("Content-Disposition"))
		checkError(err)
		fileName := params["filename"]

		newFile := FileStorage.TempPath + "/" + sessionID
		_, err = os.Create(newFile)
		checkError(err)

		f, err := os.OpenFile(newFile, os.O_APPEND|os.O_WRONLY, 0755)
		checkError(err)

		u = &uploadFile{
			file:     f,
			name:     fileName,
			tempPath: newFile,
			size:     totalSize,
		}
		u.start = time.Now()
		saveUploadFile(sessionID, u)
	} else {
		w.WriteHeader(http.StatusOK)
		if time.Since(u.status) > 10*time.Second {
			log.Printf("recieved file %s with sessionID %s transferred %d", u.name, sessionID, u.transferred)
		}
	}

	u.status = time.Now()
	_, err = u.file.Write(body)
	checkError(err)

	u.file.Sync()
	u.transferred = partTo

	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Connection", "close")
	w.Header().Set("Range", contentRange)
	w.Write([]byte(contentRange))

	if partTo >= totalSize {
		path := u.moveToPath()
		log.Printf("recieved file %s to %s with sessionID %s cost %s transferred %d",
			u.name, path, sessionID, time.Since(u.start), u.transferred)
		deleteUploadFile(sessionID)
	}
}

func (u *uploadFile) moveToPath() string {
	u.file.Close()
	filePath := FileStorage.Path + "/" + u.name
	if fileExists(filePath) {
		filePath = FileStorage.Path + "/" + time.Now().Format("20060102150405") + "-" + u.name
	}

	err := os.Rename(u.tempPath, filePath)
	checkError(err)
	return filePath
}
