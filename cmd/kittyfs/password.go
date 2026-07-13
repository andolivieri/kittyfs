package main

import (
	"bytes"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/andolivieri/kittyfs/internal/blockstore"
	"github.com/andolivieri/kittyfs/internal/crypto"
)

const passwordEnv = "KITTYFS_PASSWORD"
const corpusEnv = "KITTYFS_CORPUS"

const minPasswordLen = 1

// Reads password in this order:
// 1. KITTYFS_PASSWORD env
// 2. prompts on the terminal with no echo.
// When confirm is true (volume creation) it prompts twice
func readPassword(confirm bool) ([]byte, error) {
	if v, ok := os.LookupEnv(passwordEnv); ok {
		if len(v) < minPasswordLen {
			return nil, fmt.Errorf("%s is set but empty", passwordEnv)
		}
		return []byte(v), nil
	}

	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, fmt.Errorf("no terminal for password prompt; set %s to supply one", passwordEnv)
	}

	fmt.Fprint(os.Stderr, "password: ")
	pw, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("read password: %w", err)
	}
	if len(pw) < minPasswordLen {
		return nil, fmt.Errorf("password must be at least %d character(s)", minPasswordLen)
	}

	if confirm {
		fmt.Fprint(os.Stderr, "confirm password: ")
		pw2, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, fmt.Errorf("read password: %w", err)
		}
		if !bytes.Equal(pw, pw2) {
			return nil, fmt.Errorf("passwords do not match")
		}
	}
	return pw, nil
}

func keyDeriver(password []byte) blockstore.KeyDeriver {
	return func(salt []byte, params crypto.Argon2Params) ([crypto.KeyLen]byte, error) {
		return crypto.DeriveKey(password, salt, params), nil
	}
}
