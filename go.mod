module github.com/wilbeibi/catchup

go 1.25.0

// Four direct dependencies, all pure Go: catchup builds without cgo, so one
// `go build` cross-compiles to every platform the release targets. Each is here
// because the standard library has no answer, not for convenience.
require (
	// zstd reader for DeepSeek's compressed transcripts.
	github.com/klauspost/compress v1.19.2
	// Display width, so a CJK title lines a listing column up and truncates
	// without splitting a rune (internal/render, internal/cli).
	github.com/mattn/go-runewidth v0.0.24
	// IsTerminal and GetSize: whether to offer an update, and how wide the
	// listing may be (internal/cli, internal/render).
	golang.org/x/term v0.44.0
	// The SQLite driver, blank-imported by internal/sqlitedb, which Cursor,
	// OpenCode and ZCode read their history through. Pure Go and cgo-free,
	// which is what keeps the line above true.
	modernc.org/sqlite v1.53.0
)

require (
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.46.0 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
