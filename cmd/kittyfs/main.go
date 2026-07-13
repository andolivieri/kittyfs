package main

import (
	"flag"
	"fmt"
	"os"
)

// overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	global := flag.NewFlagSet("kittyfs", flag.ContinueOnError)
	global.Usage = usage
	dir := global.String("volume", ".kifs", "volume directory")
	corpus := global.String("corpus", "", "directory (or single PNG) of cover images; default: the embedded cats")
	if err := global.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return
		}
		os.Exit(2)
	}

	rest := global.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}

	// corpus: flag > env > embedded default.
	o := opts{volume: *dir, corpus: *corpus}
	if o.corpus == "" {
		o.corpus = os.Getenv(corpusEnv)
	}

	cmd := rest[0]
	args := rest[1:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(o, args)
	case "add":
		err = cmdAdd(o, args)
	case "get":
		err = cmdGet(o, args)
	case "ls":
		err = cmdLs(o, args)
	case "rm":
		err = cmdRm(o, args)
	case "status":
		err = cmdStatus(o, args)
	case "mount":
		err = cmdMount(o, args)
	case "cats":
		err = cmdCats(o, args)
	case "version":
		err = cmdVersion(args)
	case "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "kittyfs: unknown command %q\n", cmd)
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "kittyfs %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `kittyfs — an encrypted filesystem hidden inside a gallery of cats

Usage:
  kittyfs [OPTIONS] COMMAND

  kittyfs init            create an empty volume
  kittyfs add SRC [DEST]  import a host file into the volume
  kittyfs get PATH [OUT]  extract a file from the volume
  kittyfs ls [PATH]       list volume contents
  kittyfs rm PATH         remove a file from the volume
  kittyfs status          show volume usage, blocks, encryption
  kittyfs mount [--addr host:port] [--basic-auth]
                          serve the volume as a WebDAV drive
  kittyfs cats            print the active cover corpus
  kittyfs version         print the kittyfs version

Common options:
  --volume DIR   use the volume in DIR instead of ./.kifs.
  --corpus PATH  dress blocks as the PNGs in PATH (a directory, walked
                 recursively, or a single PNG) instead of the embedded cats.
                 Write-side only: reading a volume never needs the corpus.
Envs:
  %s - password
  %s   - default --corpus path
`, passwordEnv, corpusEnv)
}

func cmdCats(o opts, args []string) error {
	covers, err := newCovers(o)
	if err != nil {
		return err
	}
	fmt.Printf("corpus: %s\n", describeCovers(covers))
	return nil
}

func cmdVersion(args []string) error {
	fmt.Printf("kittyfs %s\n", version)
	return nil
}
