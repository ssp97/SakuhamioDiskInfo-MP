package web

import (
	"embed"
)

//go:embed static static/*
var StaticFiles embed.FS
