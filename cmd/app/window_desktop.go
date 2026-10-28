//go:build desktop

package main

import (
	"context"
	"log/slog"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// showWindow opens a native window (WebKit on macOS and Linux, WebView2
// on Windows) showing the same page the browser build serves. The page's
// requests go straight to ui.handler() inside the process, so no port is
// opened. Closing the window quits the program. It returns when the
// window closes. With --headless it opens nothing and runs until stopped,
// for running the app as a background agent.
func showWindow(ctx context.Context, ui *uiState, opts windowOptions, log *slog.Logger) {
	if opts.headless {
		<-ctx.Done()
		return
	}
	if ui == nil {
		// Without a state there is nothing to show; quitting beats leaving
		// an invisible process the user cannot find or close.
		log.Error("no window state; exiting")
		return
	}
	err := wails.Run(&options.App{
		Title:            "Relay",
		Width:            440,
		Height:           900,
		MinWidth:         360,
		MinHeight:        560,
		BackgroundColour: &options.RGBA{R: 234, G: 242, B: 251, A: 255},
		AssetServer:      &assetserver.Options{Handler: ui.handler()},
		Mac:              &mac.Options{About: &mac.AboutInfo{Title: "Relay", Message: "v" + version}},
		OnStartup: func(wctx context.Context) {
			// Ctrl-C in a terminal closes the window too.
			go func() {
				<-ctx.Done()
				wailsruntime.Quit(wctx)
			}()
		},
	})
	if err != nil {
		log.Error("window failed; exiting (use --headless to run without one)", "err", err)
	}
}
