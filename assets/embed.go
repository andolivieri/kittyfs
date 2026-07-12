// embeds the cat PNG corpus into the binary
package assets

import "embed"

//go:embed cats/*.png
var Cats embed.FS
