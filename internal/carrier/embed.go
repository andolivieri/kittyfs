package carrier

import (
	"fmt"
	"math/rand/v2"
	"sort"

	"github.com/andolivieri/kittyfs/assets"
)

// CoverSource backed by the embedded cat corpus in assets/cats.
// Which cat dresses a block is cosmetic: the blockID lives in the kiFS chunk,
// not in the choice of cover.
type EmbeddedCats struct {
	names []string
}

func NewEmbeddedCats() (*EmbeddedCats, error) {
	entries, err := assets.Cats.ReadDir("cats")
	if err != nil {
		return nil, fmt.Errorf("carrier: read embedded cats: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil, fmt.Errorf("carrier: no embedded cats found")
	}

	return &EmbeddedCats{names: names}, nil
}

func (e *EmbeddedCats) Count() int {
	return len(e.names)
}

// Returns the PNG bytes of a randomly chosen cat. blockID is ignored.
func (e *EmbeddedCats) Cover(blockID uint64) ([]byte, error) {
	name := e.names[rand.IntN(len(e.names))]
	return assets.Cats.ReadFile("cats/" + name)
}
