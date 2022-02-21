package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/bingoohuang/gg/pkg/fla9"

	"github.com/bingoohuang/goup/codec"
	"github.com/segmentio/ksuid"

	"github.com/cheggaaa/pb/v3"

	"github.com/bingoohuang/gg/pkg/flagparse"
	"github.com/bingoohuang/gg/pkg/v"

	"github.com/bingoohuang/goup"
)

type Arg struct {
	ChunkSize   uint64 `flag:",c" size:"true" val:"10MiB"`
	Coroutines  int    `flag:",t"`
	Port        int    `flag:",p" val:"2110"`
	Version     bool   `flag:",v"`
	Init        bool
	Code        fla9.StringBool `flag:"P"`
	Cipher      string          `flag:"C"`
	ServerUrl   string          `flag:",u"`
	FilePath    string          `flag:",f"`
	Rename      string          `flag:",r"`
	BearerToken string          `flag:",b"`
}

// Usage is optional for customized show.
func (a Arg) Usage() string {
	return fmt.Sprintf(`
Usage of goup:
  -b    string Bearer token for client or server, auto for server to generate a random one
  -c    string Chunk size for client, unit MB (default 10, 0 to disable chunks)
  -t    int    Threads (go-routines) for client
  -f    string Upload file path for client
  -p    int    Listening port for server
  -r    string Rename to another filename
  -u    string Server upload url for client to connect to
  -P    string Password for PAKE
  -C    string Cipher AES256: AES-256 GCM, C20P1305: ChaCha20 Poly1305
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

	c.processCode()

	if c.ServerUrl == "" {
		if c.BearerToken == "auto" {
			c.BearerToken = goup.BearerTokenGenerate()
			log.Printf("Bearer token %s generated", c.BearerToken)
		}

		if err := goup.InitServer(); err != nil {
			log.Fatalf("init goup server: %v", err)
		}
		http.HandleFunc("/", goup.Bearer(c.BearerToken, goup.ServerHandle(c.ChunkSize, c.Code.String(), c.Cipher)))
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
		goup.WithCode(c.Code.String()),
		goup.WithCipher(c.Cipher),
	)
	if err != nil {
		log.Fatalf("new goup client: %v", err)
	}

	if err := g.Start(); err != nil {
		log.Fatalf("start goup client: %v", err)
	}

	g.Wait()
}

func (a *Arg) processCode() {
	if a.Code.AsBool() {
		pwd, err := codec.ReadPassword(os.Stdin)
		if err != nil {
			log.Printf("failed to read password: %v", err)
		}
		_ = a.Code.Set(string(pwd))
	} else if a.Code.String() == "" && a.ServerUrl == "" {
		_ = a.Code.Set(ksuid.New().String())
		log.Printf("password is generate: %s", a.Code.String())
	}
}
