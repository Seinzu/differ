package web

import "embed"

// Assets contains the production frontend. Run npm run build before building Go.
//
//go:embed all:dist
var Assets embed.FS
