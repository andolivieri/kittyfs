<p align="center">
  <img src="assets/logo.png" alt="kittyFS">
</p>

<h1 align="center">kittyFS</h1>

<p align="center">
  <em>An encrypted filesystem to hide your files inside pictures of cats.</em>
</p>

## What it is

A FUSE-ish filesystem that encrypts your data with a password and scatters it
across a gallery of **valid, viewable cat PNGs**. Every block lives inside a
picture that still opens as a perfectly normal cat in any image viewer.

A volume is just a directory of those images. Given the directory and the
password, kittyFS reconstructs the original files tree, and can serve it over a
local WebDAV server, so the volume mounts as a drive and browses like any other
folder on Windows, macOS and Linux.

Written in Go. **No serious usage intended.**

## Usage

Basic usage example:

```sh
kittyfs init     # create a volume in ./.kifs
kittyfs mount    # serve it at http://localhost:8686/kittyfs/
```

`mount` also prints the copy-paste command to mount the drive on your OS

Available CLI commands and options:

```
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
  KITTYFS_PASSWORD - password
```

## Requirements & build

Requires Go 1.25+

Build with:

```sh
make build          # host binary: ./kittyfs (or kittyfs.exe)
# or
make build-all      # Linux, Windows and macOS binaries into dist/
```


## Architecture

kittyFS is built as three decoupled layers, plus a pluggable seam at the bottom:

```
cmd/kittyfs (CLI + WebDAV)  →  fs (inodes, dir tree)  →  blockstore  →  carrier
```

- **Carrier** — The PNG carrier backend
  hides a block in a custom private ancillary chunk (`kiFS`) inserted right
  before `IEND`.
- **BlockStore** — carrier-agnostic `Alloc/Read/Write/Free/Flush` over a volume
  directory, with a bump allocator, a free list, and a plaintext superblock in
  block 0.
- **fs** — inodes and a directory tree on top of the block store, serialized as
  a JSON index. Whole-file writes only.

Features:

- **Encryption**: password → Argon2id (salt and params in the
  superblock) → 256-bit key → AES-256-GCM per block, with a fresh nonce every
  write. The GCM tag doubles as per-block integrity, so tampering fails
  authentication instead of returning corrupt data.
- **Mounts as a drive, no drivers**: `mount` serves the `fs` layer over a local
  WebDAV server (`x/net/webdav`), mounted with OS-native clients on Windows,
  macOS and Linux.
- **Single self-contained binary**: pure Go, `CGO_ENABLED=0`, cat corpus
  embedded via `go:embed`

Known limitations:

- **Whole-file writes**: no partial or random writes: i.e. changing one byte
  rewrites and re-encrypts every block of the file.
- **Very easily detectable**: the `kiFS` chunk is trivial to spot.
  Also, a folder with thousands of cat pictures might raise *some* question.
- **No concurrency or crash-consistency**: expect neither. Just one
  coarse global lock, and a kill mid-flush can leave the volume inconsistent.
- **50 MB file cap on a mounted drive on Windows**: the Windows WebDAV client
  defaults to a 50 MB limit; raise it in the registry for bigger files, or use
  `add`/`get` from cli, which don't go through WebDAV.

## Disclaimer & credits

This is a **toy project**, built for playing with fs and steganographic concepts, and for the LOLs. 

It is neither audited nor hardened, and not meant for storing anything you actually care about.

Cat pictures were scraped from the awesome [https://thesecatsdonotexist.com/](https://devopstar.com/2019/02/25/generating-cats-with-stylegan-on-aws-sagemaker/) of [t04glovern](https://github.com/t04glovern) 
