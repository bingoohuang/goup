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
	chunkSize := flag.Int("c", 10, "chunk size  for client, unit MB")
	serverUrl := flag.String("u", "", "server upload url for client to connect to")
	filePath := flag.String("f", "", "upload file path for client")
	rename := flag.String("r", "", "rename to another filename")
	port := flag.Int("p", 0, "listening port for server")
	pBearerToken := flag.String("b", "", "bearer token for client or server, auto for server to generate a random one")
	flag.Parse()

	if *port > 0 {
		if *pBearerToken == "auto" {
			*pBearerToken = goup.BearerTokenGenerate()
			log.Printf("Bearer token %s generated", *pBearerToken)
		}

		goup.InitServer()
		http.HandleFunc("/", goup.Bearer(*pBearerToken, goup.UploadHandle))
		log.Printf("Listening on %d", *port)
		http.ListenAndServe(fmt.Sprintf(":%d", *port), nil)
		return
	}

	g := goup.New(*serverUrl, *filePath, *rename, &http.Client{}, uint64((*chunkSize)*(1<<20)), *pBearerToken)
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

	g.Wait()
	wg.Wait()
}
