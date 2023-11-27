package codec

import (
	"fmt"
	"io"
	"log"
	"os"

	"golang.org/x/term"
)

func ReadPassword(prompt string) ([]byte, error) {
	if prompt == "" {
		prompt = "Password"
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return readPasswordStdin(prompt)
	}
	return readPasswordTerminal(prompt + ": ")
}

func readPasswordTerminal(prompt string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	fmt.Fprintf(os.Stderr, prompt)
	// term.ReadPassword removes the trailing newline
	p, err := term.ReadPassword(fd)
	if err != nil {
		return nil, fmt.Errorf("Could not read password from terminal: %v\n", err)
	}
	fmt.Fprintf(os.Stderr, "\n")
	if len(p) == 0 {
		return nil, fmt.Errorf("Password is empty")
	}
	return p, nil
}

// readPasswordStdin reads a line from stdin.
// It exits with a fatal error on read error or empty result.
func readPasswordStdin(prompt string) ([]byte, error) {
	log.Printf("Reading %s from stdin", prompt)
	p, err := readLineUnbuffered(os.Stdin)
	if err != nil {
		return nil, err
	}
	if len(p) == 0 {
		return nil, fmt.Errorf("got empty %s from stdin", prompt)
	}
	return p, nil
}

// readLineUnbuffered reads single bytes from "r" util it gets "\n" or EOF.
// The returned string does NOT contain the trailing "\n".
func readLineUnbuffered(r io.Reader) (l []byte, err error) {
	b := make([]byte, 1)
	for {
		if len(l) > maxPasswordLen {
			return nil, fmt.Errorf("fatal: maximum password length of %d bytes exceeded", maxPasswordLen)
		}
		n, err := r.Read(b)
		if err == io.EOF {
			return l, nil
		}
		if err != nil {
			return nil, fmt.Errorf("readLineUnbuffered: %v", err)
		}
		if n == 0 {
			continue
		}
		if b[0] == '\n' {
			return l, nil
		}
		l = append(l, b...)
	}
}

const (
	// 2kB limit like EncFS
	maxPasswordLen = 2048
)
