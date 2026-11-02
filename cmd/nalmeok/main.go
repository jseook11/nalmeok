package main

import (
	"io/fs"
	"log"

	"github.com/jseook11/nalmeok/frontend"
	"github.com/jseook11/nalmeok/internal/app"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	itemsPath, err := config.ItemsPath()
	if err != nil {
		log.Fatal(err)
	}
	history, err := store.Load(itemsPath)
	if err != nil {
		log.Fatal(err)
	}

	application := app.New(cfg, history, app.NewStubRunner(history))

	assets, err := fs.Sub(frontend.Assets, "dist")
	if err != nil {
		log.Fatal(err)
	}

	err = wails.Run(&options.App{
		Title:     "날먹",
		Width:     480,
		Height:    680,
		MinWidth:  420,
		MinHeight: 560,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop: true,
		},
		Mac: &mac.Options{
			TitleBar:   mac.TitleBarDefault(),
			Appearance: mac.DefaultAppearance,
		},
		OnStartup:  application.Startup,
		OnShutdown: application.Shutdown,
		Bind:       []interface{}{application},
	})
	if err != nil {
		log.Fatal(err)
	}
}
