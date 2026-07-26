// Package web embeds the NodeOS web UI into the nodeosd binary.
package web

import "embed"

//go:embed index.html app.js style.css world.js
var Files embed.FS
