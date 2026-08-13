package ui

import "embed"

// Files is produced by the Vite build and compiled into the single Kkiit binary.
//
//go:embed dist/*
var Files embed.FS
