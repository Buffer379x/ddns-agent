package web

import "embed"

//go:embed all:lang css js img index.html favicon.png
var FS embed.FS
