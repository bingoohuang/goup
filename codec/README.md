## How do you encrypt large files / byte streams in Go?

https://stackoverflow.com/questions/49546567/how-do-you-encrypt-large-files-byte-streams-in-go/

I have some large files I would like to AES encrypt before sending over the wire or saving to disk. While it seems
possible to [encrypt streams](https://golang.org/src/crypto/cipher/example_test.go#L335), there seems to
be [warnings](https://stackoverflow.com/questions/39378051/making-gcm-cbc-ciphers-streamable-in-golang)
against [doing this](https://www.imperialviolet.org/2014/06/27/streamingencryption.html) and instead people recommend
splitting the files into chunks and using GCM or crypto/nacl/secretbox.

Processing streams of data is more difficult due to the authenticity requirement. We can’t encrypt-then-MAC: by it’s
nature, we usually don’t know the size of a stream. We can’t send the MAC after the stream is complete, as that usually
is indicated by the stream being closed. We can’t decrypt a stream on the fly, because we have to see the entire
ciphertext in order to check the MAC. Attempting to secure a stream adds enormous complexity to the problem, with no
good answers. The solution is to break the stream into discrete chunks, and treat them as messages.

https://leanpub.com/gocrypto/read
Files are segmented into 4KiB blocks. Each block gets a fresh random 128 bit IV each time it is modified. A 128-bit
authentication tag (GHASH) protects each block from modifications.

https://nuetzlich.net/gocryptfs/forward_mode_crypto/
If a large amount of data is decrypted it is not always possible to buffer all decrypted data until the authentication
tag is verified. Splitting the data into small chunks fixes the problem of deferred authentication checks but introduces
a new one. The chunks can be reordered... ...because every chunk is encrypted separately. Therefore the order of the
chunks must be encoded somehow into the chunks itself to be able to detect rearranging any number of chunks.

https://github.com/minio/sio
Can anyone with actual cryptography experience point me in the right direction?

Update I realized after asking this question that there is a difference between simply not being able to fit the whole
byte stream into memory (encrypting a 10GB file) and the byte stream also being an unknown length that could continue
long past the need for the stream's start to be decoded (an 24-hour live video stream).

I am mostly interested in large blobs where the end of the stream can be reached before the beginning needs to be
decoded. In other words, encryption that does not require the whole plaintext/ciphertext to be loaded into memory at the
same time.