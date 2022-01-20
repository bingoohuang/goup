package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bingoohuang/gg/pkg/flagparse"
	"github.com/bingoohuang/gg/pkg/v"

	"github.com/bingoohuang/goup"
	"github.com/cheggaaa/pb/v3"
)

type Arg struct {
	ChunkSize   int    `flag:",c" val:"10"`
	ServerUrl   string `flag:",u"`
	FilePath    string `flag:",f"`
	Rename      string `flag:",r"`
	Port        int    `flag:",p"`
	BearerToken string `flag:",b"`
	Version     bool   `flag:",v"`
	Init        bool
}

// Usage is optional for customized show.
func (a Arg) Usage() string {
	return fmt.Sprintf(`
Usage of goup:
  -b string bearer token for client or server, auto for server to generate a random one
  -c int chunk size  for client, unit MB (default 10)
  -f string upload file path for client
  -p int listening port for server
  -r string rename to another filename
  -u string server upload url for client to connect to
  -v bool show version
  -init bool create init ctl shell script`)
}

// VersionInfo is optional for customized version.
func (a Arg) VersionInfo() string { return v.Version() }

func main() {
	c := &Arg{}
	flagparse.Parse(c)
	chunkSize := uint64((c.ChunkSize) * (1 << 20))

	if c.Port > 0 {
		if c.BearerToken == "auto" {
			c.BearerToken = goup.BearerTokenGenerate()
			log.Printf("Bearer token %s generated", c.BearerToken)
		}

		if err := goup.InitServer(); err != nil {
			log.Fatalf("init goup server: %v", err)
		}
		http.HandleFunc("/", goup.Bearer(c.BearerToken, goup.ServerHandle(chunkSize)))
		log.Printf("Listening on %d", c.Port)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", c.Port), nil); err != nil {
			log.Printf("listen: %v", err)
		}
		return
	}

	g, err := goup.New(c.ServerUrl, c.FilePath, c.Rename, &http.Client{}, chunkSize, c.BearerToken)
	if err != nil {
		log.Fatalf("new goup client: %v", err)
	}
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
