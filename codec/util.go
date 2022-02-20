package codec

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh/terminal"
)

func ReadPassword(src *os.File) ([]byte, error) {
	//state, err := terminal.GetState(int(src.Fd()))
	//if err != nil {
	//	return nil, fmt.Errorf("failed to read password: %w", err)
	//}

	// handle user termination
	//sigChan := make(chan os.Signal, 1)
	//signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	//go func() {
	//	<-sigChan
	//
	//	stat, _ := terminal.GetState(int(src.Fd()))
	//	if stat != nil && *stat != *state {
	//		fmt.Fprintln(src, "\nFailed to read password: Interrupted")
	//	}
	//	terminal.Restore(int(src.Fd()), state)
	//}()

	fmt.Fprint(src, "Enter password:")
	p, err := terminal.ReadPassword(int(src.Fd()))
	if err != nil {
		return nil, fmt.Errorf("failed to read password: %w", err)
	}
	fmt.Fprintln(src, "")

	return p, err
}
