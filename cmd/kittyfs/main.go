package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/andolivieri/kittyfs/internal/carrier"
)

// overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	global := flag.NewFlagSet("kittyfs", flag.ContinueOnError)
	global.Usage = usage
	dir := global.String("volume", ".kifs", "volume directory")
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

	cmd := rest[0]
	args := rest[1:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(*dir, args)
	case "add":
		err = cmdAdd(*dir, args)
	case "get":
		err = cmdGet(*dir, args)
	case "ls":
		err = cmdLs(*dir, args)
	case "rm":
		err = cmdRm(*dir, args)
	case "status":
		err = cmdStatus(*dir, args)
	case "mount":
		err = cmdMount(*dir, args)
	case "cats":
		err = cmdCats(args)
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
  kittyfs [--volume DIR] init            create an empty volume
  kittyfs [--volume DIR] add SRC [DEST]  import a host file into the volume
  kittyfs [--volume DIR] get PATH [OUT]  extract a file from the volume
  kittyfs [--volume DIR] ls [PATH]       list volume contents
  kittyfs [--volume DIR] rm PATH         remove a file from the volume
  kittyfs [--volume DIR] status          show volume usage, blocks, encryption
  kittyfs [--volume DIR] mount [--addr host:port] [--basic-auth]
                                         serve the volume as a WebDAV drive
  kittyfs cats                           print the embedded cat corpus size
  kittyfs version                        print the kittyfs version
Envs:
  %s - password
`, passwordEnv)
}

func cmdCats(args []string) error {
	cats, err := carrier.NewEmbeddedCats()
	if err != nil {
		return err
	}
	fmt.Printf("embedded cats: %d\n", cats.Count())
	return nil
}

func cmdVersion(args []string) error {
	fmt.Printf("kittyfs %s\n", version)
	return nil
}
