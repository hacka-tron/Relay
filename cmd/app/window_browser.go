//go:build !desktop

package main

import (
	"context"
	"log/slog"
	"os/exec"
	"runtime"
)

// showWindow serves the page on a loopback address and opens it in the
// default browser, then waits until the program is stopped. This is the
// build used by `go build`, the tests and the demo scripts: pure Go, no
// native toolchain needed.
func showWindow(ctx context.Context, ui *uiState, opts windowOptions, log *slog.Logger) {
	if ui != nil && opts.addr != "" && !opts.headless {
		if url := serveUI(opts.addr, ui, log); url != "" {
			log.Info("window", "url", url)
			if opts.openBrowser {
				openBrowser(url)
			}
		}
	}
	<-ctx.Done()
}

// openBrowser opens url in the user's default browser. Failure is fine;
// the URL is logged either way.
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	cmd.Start()
}
