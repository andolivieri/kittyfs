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

type opts struct {
	volume string
	corpus string
}

// Empty corpus => use embedded cats; otherwise the user's own directory
func newCovers(o opts) (carrier.CoverSource, error) {
	if o.corpus == "" {
		return carrier.NewEmbeddedCats()
	}
	return carrier.NewDirCovers(o.corpus)
}

func newCarrier(o opts) (carrier.Carrier, error) {
	covers, err := newCovers(o)
	if err != nil {
		return nil, err
	}
	return carrier.NewPNGCarrier(covers), nil
}

func describeCovers(covers carrier.CoverSource) string {
	d, ok := covers.(*carrier.DirCovers)
	if !ok {
		return fmt.Sprintf("embedded (%d cats)", covers.Count())
	}
	s := fmt.Sprintf("dir %s (%d PNGs", d.Root(), d.Count())
	if n := d.Skipped(); n > 0 {
		s += fmt.Sprintf(", %d non-PNG files skipped", n)
	}
	return s + ")"
}

// prompts for the password and re-derives the key
func openStore(o opts) (*blockstore.DirStore, error) {
	c, err := newCarrier(o)
	if err != nil {
		return nil, err
	}
	pw, err := readPassword(false)
	if err != nil {
		return nil, err
	}
	return blockstore.Open(o.volume, c, keyDeriver(pw))
}

func openVolume(o opts) (*fs.VolumeFS, error) {
	store, err := openStore(o)
	if err != nil {
		return nil, err
	}
	return fs.Open(store)
}

func cmdInit(o opts, args []string) error {
	c, err := newCarrier(o)
	if err != nil {
		return err
	}
	pw, err := readPassword(true)
	if err != nil {
		return err
	}
	store, err := blockstore.Create(o.volume, c, crypto.DefaultArgon2Params(), keyDeriver(pw))
	if err != nil {
		return err
	}
	vfs := fs.Create(store)
	if err := vfs.Flush(); err != nil {
		return err
	}
	fmt.Printf("initialized encrypted kittyfs volume at %s\n", o.volume)
	return nil
}

func cmdAdd(o opts, rest []string) error {
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
	vfs, err := openVolume(o)
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

func cmdGet(o opts, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("usage: kittyfs [--volume DIR] get PATH [OUT]")
	}
	srcPath := rest[0]
	vfs, err := openVolume(o)
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

func cmdLs(o opts, rest []string) error {
	target := "/"
	if len(rest) >= 1 {
		target = rest[0]
	}
	vfs, err := openVolume(o)
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

func cmdRm(o opts, rest []string) error {
	if len(rest) < 1 {
		return fmt.Errorf("usage: kittyfs [--volume DIR] rm PATH")
	}
	vfs, err := openVolume(o)
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
