package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

// The only network call catchup makes, and only when the user asks for
// --version. Everything else stays local, which is the point of the tool.
//
// Someone who installed once and never upgraded has no way to learn a newer
// release exists: `go install` and the curl installer both leave a binary that
// never speaks up. `catchup --version` is the moment they are already asking
// the question, so it is the one place worth answering it.
//
// Deliberately not here: a cached weekly check, a background goroutine, a
// `catchup update` that rewrites the binary, and anything in SKILL.md. Those
// can be added if this proves too quiet; none of them can be removed once
// people rely on them.

const latestReleaseURL = "https://github.com/wilbeibi/catchup/releases/latest"

// latestTag resolves the newest published tag by following the release
// redirect — the same trick scripts/install.sh uses, so the installer and this
// check can never disagree about what "latest" means. No API token, no rate
// limit, no JSON. Swapped in tests.
var latestTag = fetchLatestTag

func fetchLatestTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	tag := path.Base(res.Request.URL.Path)
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("release redirect ended at %q, not a tag", tag)
	}
	return tag, nil
}

// noticeUpdate prints one line to stderr when a newer release exists, and
// nothing at all otherwise. Every failure is silent: offline, rate-limited,
// and air-gapped runs must print exactly what they printed before this
// existed. tty is false unless both output streams are terminals — when
// catchup is piped into an agent there is nobody to read a notice, and the
// request is pure latency.
func noticeUpdate(ctx context.Context, version string, tty bool, stderr io.Writer) {
	if !tty || version == "dev" || os.Getenv("CATCHUP_NO_UPDATE_CHECK") != "" {
		return
	}
	tag, err := latestTag(ctx)
	if err != nil {
		return
	}
	if !newer(tag, version) {
		return
	}
	fmt.Fprintf(stderr, "%s is available: https://github.com/wilbeibi/catchup/releases\n", tag)
}

// newer reports whether tag is a later release than the running build. Both
// have to be a plain X.Y.Z: GitHub's "latest" skips prereleases, so a build of
// 0.9.0-rc.1 would otherwise be told that the 0.8.0 it is ahead of is an
// upgrade. Anything unparseable — a prerelease, a go install pseudo-version, a
// git describe suffix — is not comparable, and not comparable means silent.
func newer(tag, version string) bool {
	t, ok := parseVersion(tag)
	if !ok {
		return false
	}
	v, ok := parseVersion(version)
	if !ok {
		return false
	}
	return slices.Compare(t, v) > 0
}

func parseVersion(s string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != 3 {
		return nil, false
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// isTerminal reports whether w is a terminal. os.ModeCharDevice is not the
// test: /dev/null is a character device too, so `--version > /dev/null` would
// pass it and spend a request nobody asked for.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
