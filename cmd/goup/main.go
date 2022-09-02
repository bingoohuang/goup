package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/k0kubun/go-ansi"
	"github.com/schollz/progressbar/v3"

	ggcodec "github.com/bingoohuang/gg/pkg/codec"
	"github.com/bingoohuang/gg/pkg/fla9"

	"github.com/bingoohuang/goup/codec"
	"github.com/segmentio/ksuid"

	"github.com/bingoohuang/gg/pkg/flagparse"
	"github.com/bingoohuang/gg/pkg/v"

	"github.com/bingoohuang/goup"
)

type Arg struct {
	ChunkSize   uint64 `flag:",c" size:"true" val:"10MiB"`
	LimitRate   uint64 `flag:",L" size:"true" val:"0"`
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
	Paths       []string        `flag:"path"`
}

// Usage is optional for customized show.
func (a Arg) Usage() string {
	return fmt.Sprintf(`
Usage of goup:
  -b    string Bearer token for client or server, auto for server to generate a random one
  -c    string Chunk size for client (default 10MB, 0 to disable chunks)
  -t    int    Threads (go-routines) for client
  -f    string Upload file path for client
  -p    int    Listening port for server
  -r    string Rename to another filename
  -u    string Server upload url for client to connect to
  -P    string Password for PAKE
  -L    string Limit rate /s, like 10K for limit 10K/s
  -C    string Cipher AES256: AES-256 GCM, C20P1305: ChaCha20 Poly1305
  -v    bool   Show version
  -path /short=/short.zip Short URLs
  -init bool   Create init ctl shell script`)
}

// VersionInfo is optional for customized version.
func (a Arg) VersionInfo() string { return v.Version() }

func main() {
	c := &Arg{}
	flagparse.Parse(c)
	c.processCode()
	log.Printf("Args: %s", ggcodec.Json(c))

	if c.ServerUrl == "" {
		if c.BearerToken == "auto" {
			c.BearerToken = goup.BearerTokenGenerate()
			log.Printf("Bearer token %s generated", c.BearerToken)
		}

		if err := goup.InitServer(); err != nil {
			log.Fatalf("init goup server: %v", err)
		}
		http.HandleFunc("/", goup.Bearer(c.BearerToken, goup.ServerHandle(c.Code.String(), c.Cipher, c.ChunkSize, c.LimitRate, c.Paths)))
		log.Printf("Listening on %d", c.Port)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", c.Port), nil); err != nil {
			log.Printf("E! listen failed: %v", err)
		}
		return
	}

	g, err := goup.New(c.ServerUrl,
		goup.WithFullPath(c.FilePath),
		goup.WithRename(c.Rename),
		goup.WithBearer(c.BearerToken),
		goup.WithChunkSize(c.ChunkSize),
		goup.WithProgress(newSchollzProgressbar()),
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
	if a.Code.Exists && a.Code.Val == "" {
		pwd, err := codec.ReadPassword(os.Stdin)
		if err != nil {
			log.Printf("E! read password failed: %v", err)
		}
		_ = a.Code.Set(string(pwd))
	} else if a.Code.Val == "" && a.ServerUrl == "" {
		_ = a.Code.Set(ksuid.New().String())
		log.Printf("password is generate: %s", a.Code.String())
	}
}

type schollzProgressbar struct {
	bar *progressbar.ProgressBar
}

func (s *schollzProgressbar) Start(value uint64) {
	s.bar = progressbar.NewOptions64(
		int64(value),
		progressbar.OptionSetWriter(ansi.NewAnsiStdout()),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(true),
		progressbar.OptionSetWidth(10),
		progressbar.OptionShowCount(),
		progressbar.OptionOnCompletion(func() {
			fmt.Printf("\n")
		}),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
	)
}

func (s *schollzProgressbar) Add(value uint64) {
	_ = s.bar.Add64(int64(value))
}

func (s schollzProgressbar) Finish() {
	s.bar.Finish()
}

func newSchollzProgressbar() *schollzProgressbar {
	return &schollzProgressbar{}
}
