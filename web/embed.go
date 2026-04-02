package web

import "embed"

//go:embed all:lang css js img index.html favicon.png favicon.webp
var FS embed.FS
