package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"
)

// readSecret prompts on stderr and reads a secret from stdin. When stdin is a
// terminal the input is read without echo, so the value never lands in shell
// history, ps output, or the terminal scrollback. When stdin is not a terminal
// (piped input, e.g. from a secrets manager) it reads a single line instead.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr) // ReadPassword swallows the newline
		if err != nil {
			return "", fmt.Errorf("failed to read secret: %w", err)
		}
		return string(b), nil
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("failed to read secret from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}
