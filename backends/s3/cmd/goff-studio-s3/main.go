package main

import (
	"embed"

	_ "github.com/go-feature-flag/studio/backends/s3"
	"github.com/go-feature-flag/studio/internal/app"
)

//go:embed all:dist
var dist embed.FS

func main() { app.Run(dist) }
