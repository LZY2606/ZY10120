package server

import (
	"embed"
	"io/fs"
)

// webFS 内嵌页面与静态资源，服务为单一自包含二进制。
//
//go:embed web/*.html web/static/*
var webFS embed.FS

func staticFS() fs.FS {
	sub, err := fs.Sub(webFS, "web/static")
	if err != nil {
		panic(err)
	}
	return sub
}
