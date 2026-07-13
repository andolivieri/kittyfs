package carrier

import (
	"fmt"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CoverSource backed by a user-supplied directory of PNGs ("bring your own
// cats")
type DirCovers struct {
	root    string
	paths   []string // absolute, sorted
	skipped int
}

// Walks path and collects every  PNG in it
func NewDirCovers(path string) (*DirCovers, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("carrier: corpus %q: %w", path, err)
	}

	var candidates []string
	skipped := 0

	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".png") {
				skipped++
				return nil
			}
			candidates = append(candidates, p)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("carrier: corpus %q: %w", path, err)
		}
	} else if strings.EqualFold(filepath.Ext(path), ".png") {
		candidates = append(candidates, path)
	} else {
		skipped++
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("carrier: corpus %q: no PNG files found (%d files skipped)", path, skipped)
	}

	paths := make([]string, 0, len(candidates))
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("carrier: corpus %q: %w", p, err)
		}
		if err := validateCover(abs); err != nil {
			return nil, err
		}
		paths = append(paths, abs)
	}

	sort.Strings(paths)

	return &DirCovers{root: path, paths: paths, skipped: skipped}, nil
}

func (d *DirCovers) Count() int { return len(d.paths) }

func (d *DirCovers) Root() string { return d.root }

func (d *DirCovers) Skipped() int { return d.skipped }

func (d *DirCovers) Cover(blockID uint64) ([]byte, error) {
	path := d.paths[rand.IntN(len(d.paths))]
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("carrier: read cover %q: %w", path, err)
	}
	return b, nil
}

func validateCover(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("carrier: corpus %q: %w", path, err)
	}
	defer f.Close()

	// Header-only: reads IHDR, not the pixels.
	if _, err := png.DecodeConfig(f); err != nil {
		return fmt.Errorf("carrier: corpus %q: not a valid PNG: %w", path, err)
	}

	// must not already be a kittyFS block
	carries, err := HasKiFSChunk(f)
	if err != nil {
		return fmt.Errorf("carrier: corpus %q: %w", path, err)
	}
	if carries {
		return fmt.Errorf("carrier: corpus %q: this is a kittyfs block, not a cover", path)
	}
	return nil
}
