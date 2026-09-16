// Package web embeds the built frontend.
package web

import "embed"

// Build holds the SvelteKit static build output.
//
//go:embed all:build
var Build embed.FS
