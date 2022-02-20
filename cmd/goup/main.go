package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cheggaaa/pb/v3"

	"github.com/bingoohuang/gg/pkg/flagparse"
	"github.com/bingoohuang/gg/pkg/v"

	"github.com/bingoohuang/goup"
)

type Arg struct {
	Code        string `flag:"code" val:"abc123"`
	ChunkSize   uint64 `flag:",c" size:"true" val:"10MiB"`
	Coroutines  int    `flag:",t"`
	ServerUrl   string `flag:",u"`
	FilePath    string `flag:",f"`
	Rename      string `flag:",r"`
	Port        int    `flag:",p" val:"2110"`
	BearerToken string `flag:",b"`
	Version     bool   `flag:",v"`
	Init        bool
}

// Usage is optional for customized show.
func (a Arg) Usage() string {
	return fmt.Sprintf(`
Usage of goup:
  -b    string Bearer token for client or server, auto for server to generate a random one
  -c    string Chunk size for client, unit MB (default 10)
  -t    int    Threads (go-routines) for client
  -f    string Upload file path for client
  -p    int    Listening port for server
  -r    string Rename to another filename
  -u    string Server upload url for client to connect to
  -code string Codephrase
  -v    bool   Show version
  -init bool   Create init ctl shell script`)
}

// VersionInfo is optional for customized version.
func (a Arg) VersionInfo() string { return v.Version() }

type pbProgress struct {
	bar *pb.ProgressBar
}

func (p pbProgress) Start(value uint64) {
	p.bar.SetTotal(int64(value))
	p.bar.Start()
}
func (p pbProgress) Add(value uint64) { p.bar.Add64(int64(value)) }
func (p pbProgress) Finish()          { p.bar.Finish() }

func main() {
	c := &Arg{}
	flagparse.Parse(c)

	if c.ServerUrl == "" {
		if c.BearerToken == "auto" {
			c.BearerToken = goup.BearerTokenGenerate()
			log.Printf("Bearer token %s generated", c.BearerToken)
		}

		if err := goup.InitServer(); err != nil {
			log.Fatalf("init goup server: %v", err)
		}
		http.HandleFunc("/", goup.Bearer(c.BearerToken, goup.ServerHandle(c.ChunkSize, c.Code)))
		log.Printf("Listening on %d", c.Port)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", c.Port), nil); err != nil {
			log.Printf("listen: %v", err)
		}
		return
	}
	bar := pb.New(0)
	bar.SetRefreshRate(100 * time.Millisecond)
	bar.Set(pb.Bytes, true)
	g, err := goup.New(c.ServerUrl,
		goup.WithFullPath(c.FilePath),
		goup.WithRename(c.Rename),
		goup.WithBearer(c.BearerToken),
		goup.WithChunkSize(c.ChunkSize),
		goup.WithProgress(&pbProgress{bar: bar}),
		goup.WithCoroutines(c.Coroutines),
		goup.WithCode(c.Code),
	)
	if err != nil {
		log.Fatalf("new goup client: %v", err)
	}

	if err := g.Start(); err != nil {
		log.Fatalf("start goup client: %v", err)
	}

	g.Wait()
}
