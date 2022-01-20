package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bingoohuang/goup"
	"github.com/cheggaaa/pb/v3"
)

func main() {
	serverUrl := flag.String("u", "", "server upload url")
	filePath := flag.String("f", "", "upload file path")
	isServer := flag.Bool("s", false, "start as server")
	flag.Parse()

	if *isServer {
		http.HandleFunc("/", goup.HTTPHandler)
		fmt.Println("Listening on :2110")
		http.ListenAndServe(":2110", nil)
		return
	}

	httpClient := &http.Client{}
	const chunkSize = 1 * (1 << 20) // 1MB
	g := goup.New(*serverUrl, *filePath, httpClient, chunkSize)

	bar := pb.New(int(g.Status.Size))
	bar.SetRefreshRate(time.Millisecond)
	bar.Set(pb.Bytes, true)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		log.Printf("Upload %s started", g.ID)
		defer func() {
			bar.SetCurrent(int64(g.Status.Size))
			bar.Finish()
			wg.Done()
			log.Printf("Upload %s completed", g.ID)
		}()

		bar.Start()
		for g.Status.SizeTransferred < g.Status.Size {
			bar.SetCurrent(int64(g.Status.SizeTransferred))
			time.Sleep(time.Millisecond)
		}
	}()

	g.Start()
	g.Wait()
	wg.Wait()
}
