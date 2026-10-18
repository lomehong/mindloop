module github.com/lomehong/mindloop

go 1.26.0

require github.com/spf13/cobra v1.10.2

require (
	github.com/fsnotify/fsnotify v1.10.1
	golang.org/x/sys v0.48.0
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

// 开发期使用本机 Nuwa 仓库的青鸟 SDK；正式发布后替换为 GitHub module
