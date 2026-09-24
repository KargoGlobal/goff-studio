package main

import (
	"embed"

	"github.com/go-feature-flag/studio/internal/app"
)

//go:embed all:dist
var dist embed.FS

func main() { app.Run(dist) }
