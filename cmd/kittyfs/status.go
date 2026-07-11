package main

import (
	"fmt"
	"math"

	"github.com/andolivieri/kittyfs/internal/carrier"
	"github.com/andolivieri/kittyfs/internal/fs"
)

func cmdStatus(dir string, args []string) error {
	store, err := openStore(dir)
	if err != nil {
		return err
	}
	bs, err := store.Stats()
	if err != nil {
		return err
	}
	vfs, err := fs.Open(store)
	if err != nil {
		return err
	}
	fst := vfs.Stats()

	fmt.Printf("volume:  %s\n", bs.Dir)
	fmt.Printf("format:  version %d\n", bs.FormatVersion)
	fmt.Println()

	fmt.Println("contents")
	fmt.Printf("  files            %d\n", fst.Files)
	fmt.Printf("  directories      %d\n", fst.Dirs)
	fmt.Printf("  logical size     %s\n", humanBytes(fst.Bytes))
	fmt.Println()

	fmt.Println("blocks")
	fmt.Printf("  block size       %s\n", humanBytes(int64(bs.BlockSize)))
	fmt.Printf("  allocated        %d  (%d file, %d fs-index)\n",
		bs.AllocatedBlocks, fst.FileBlocks, bs.IndexBlocks)
	fmt.Printf("  free (reusable)  %d\n", bs.FreeBlocks)
	fmt.Printf("  next block id    %d\n", bs.NextBlockID)
	fmt.Println()

	fmt.Println("on disk")
	fmt.Printf("  cat images       %d  (incl. the superblock cat)\n", bs.CarrierFiles)
	fmt.Printf("  total size       %s\n", humanBytes(bs.CarrierBytes))
	if fst.Bytes > 0 {
		pct := float64(bs.CarrierBytes-fst.Bytes) / float64(fst.Bytes) * 100
		fmt.Printf("  overhead         %s\n", humanPercent(pct))
	}
	fmt.Println()

	fmt.Println("encryption")
	if bs.Encrypted {
		fmt.Println("  data blocks      AES-256-GCM (per-block nonce + tag)")
		fmt.Println("  key derivation   Argon2id")
		fmt.Printf("  argon2 params    time=%d memory=%s threads=%d\n",
			bs.Argon2.Time, humanBytes(int64(bs.Argon2.Memory)*1024), bs.Argon2.Threads)
		fmt.Printf("  salt             %d bytes (per volume)\n", bs.SaltLen)
		fmt.Println("  superblock       plaintext (carries the KDF bootstrap)")
	} else {
		fmt.Println("  data blocks      none — this volume is plaintext")
	}
	fmt.Println()

	cats, err := carrier.NewEmbeddedCats()
	if err != nil {
		return err
	}
	fmt.Println("carrier")
	fmt.Printf("  kind             PNG (kiFS chunk before IEND)\n")
	fmt.Printf("  cat corpus       %d embedded images\n", cats.Count())
	return nil
}

func humanPercent(pct float64) string {
	switch a := math.Abs(pct); {
	case a < 10:
		return fmt.Sprintf("%.1f%%", pct)
	case a < 100000:
		return fmt.Sprintf("%.0f%%", pct)
	default:
		return fmt.Sprintf("%.0fk%%", pct/1000)
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
