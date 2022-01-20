# goup

It's a Go library providing multiple simultaneous and resume-able uploads.

Library is designed to introduce fault-tolerance into the upload of large files through HTTP. This is done by splitting
large file into small chunks; whenever the upload of a chunk fails, uploading is retried until the procedure completes.
This allows uploads to automatically resume uploading after a network connection is lost either locally or to the
server. Additionally, it allows users to pause, resume and even recover uploads without losing state.

### Usage

1. Installation `go install https://github.com/bingoohuang/goup`
1. At the server, `goup -p 2110`
2. At the client, `goup -u http://a.b.c:2110/ -f 246.png`

