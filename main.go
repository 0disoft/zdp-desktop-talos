package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/bootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	localDataRoot, rootErr := os.UserCacheDir()
	var vaultService *wailsapi.VaultService
	if rootErr != nil {
		vaultService = wailsapi.NewVaultService(nil, rootErr)
	} else {
		creator, err := bootstrap.NewVaultCreator(filepath.Join(localDataRoot, "0disoft", "Talos Agent"))
		vaultService = wailsapi.NewVaultService(creator, err)
	}
	app := application.New(application.Options{
		Name:        "zdp-desktop-talos",
		Description: "기억 기반 로컬 코딩 에이전트",
		Services: []application.Service{
			application.NewService(&wailsapi.HealthService{}),
			application.NewService(vaultService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:     "Talos Agent",
		Width:     1180,
		Height:    760,
		MinWidth:  920,
		MinHeight: 620,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 48,
			Backdrop:                application.MacBackdropTranslucent,
			TitleBar:                application.MacTitleBarHiddenInset,
		},
		BackgroundColour: application.NewRGB(8, 10, 15),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
