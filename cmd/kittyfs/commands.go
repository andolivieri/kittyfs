package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/andolivieri/kittyfs/internal/blockstore"
	"github.com/andolivieri/kittyfs/internal/carrier"
	"github.com/andolivieri/kittyfs/internal/crypto"
	"github.com/andolivieri/kittyfs/internal/fs"
)

func newCarrier() (carrier.Carrier, error) {
	cats, err := carrier.NewEmbeddedCats()
	if err != nil {
		return nil, err
	}
	return carrier.NewPNGCarrier(cats), nil
}

// prompts for the password and re-derives the key
func openStore(dir string) (*blockstore.DirStore, error) {
	c, err := newCarrier()
	if err != nil {
		return nil, err
	}
	pw, err := readPassword(false)
	if err != nil {
		return nil, err
	}
	return blockstore.Open(dir, c, keyDeriver(pw))
}

func openVolume(dir string) (*fs.VolumeFS, error) {
	store, err := openStore(dir)
	if err != nil {
		return nil, err
	}
	return fs.Open(store)
}

func cmdInit(dir string, args []string) error {
	c, err := newCarrier()
	if err != nil {
		return err
	}
	pw, err := readPassword(true)
	if err != nil {
		return err
	}
	store, err := blockstore.Create(dir, c, crypto.DefaultArgon2Params(), keyDeriver(pw))
	if err != nil {
		return err
	}
	vfs := fs.Create(store)
	if err := vfs.Flush(); err != nil {
		return err
	}
	fmt.Printf("initialized encrypted kittyfs volume at %s\n", dir)
	return nil
}

func cmdAdd(dir string, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("usage: kittyfs [--volume DIR] add SRC [DEST]")
	}
	src := rest[0]
	dest := ""
	if len(rest) >= 2 {
		dest = rest[1]
	} else {
		dest = filepath.Base(src)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	vfs, err := openVolume(dir)
	if err != nil {
		return err
	}
	if err := vfs.WriteFile(dest, data); err != nil {
		return err
	}
	if err := vfs.Flush(); err != nil {
		return err
	}
	fmt.Printf("added %s -> %s (%d bytes)\n", src, dest, len(data))
	return nil
}

func cmdGet(dir string, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("usage: kittyfs [--volume DIR] get PATH [OUT]")
	}
	srcPath := rest[0]
	vfs, err := openVolume(dir)
	if err != nil {
		return err
	}
	data, err := vfs.ReadFile(srcPath)
	if err != nil {
		return err
	}
	if len(rest) >= 2 {
		if err := os.WriteFile(rest[1], data, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (%d bytes)\n", rest[1], len(data))
		return nil
	}
	_, err = os.Stdout.Write(data)
	return err
}

func cmdLs(dir string, rest []string) error {
	target := "/"
	if len(rest) >= 1 {
		target = rest[0]
	}
	vfs, err := openVolume(dir)
	if err != nil {
		return err
	}
	entries, err := vfs.List(target)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir {
			fmt.Printf("%-30s  <dir>\n", e.Name+"/")
		} else {
			fmt.Printf("%-30s  %10d\n", e.Name, e.Size)
		}
	}
	return nil
}

func cmdRm(dir string, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("usage: kittyfs [--volume DIR] rm PATH")
	}
	vfs, err := openVolume(dir)
	if err != nil {
		return err
	}
	if err := vfs.Remove(rest[0]); err != nil {
		return err
	}
	if err := vfs.Flush(); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", rest[0])
	return nil
}
