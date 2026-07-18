package main

import (
	"context"
	"embed"
	"errors"
	"log"
	"os"
	"path/filepath"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/bootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	localDataRoot, rootErr := os.UserCacheDir()
	var vaultService *wailsapi.VaultService
	var executionFactory *bootstrap.ExecutionFactory
	var executionInitializationError error
	inspector, inspectorErr := gitcli.New()
	workspaceService := wailsapi.NewWorkspaceService(inspector, inspectorErr)
	var singleInstance *application.SingleInstanceOptions
	if rootErr != nil {
		vaultService = wailsapi.NewVaultService(nil, rootErr)
		executionInitializationError = rootErr
	} else {
		root := filepath.Join(localDataRoot, "0disoft", "Talos Agent")
		creator, creatorErr := bootstrap.NewVaultCreator(root)
		instanceKey, instanceErr := bootstrap.LoadOrCreateInstanceKey(context.Background(), root)
		if instanceErr == nil {
			singleInstance = &application.SingleInstanceOptions{
				UniqueID:      "com.0disoft.talos-agent",
				EncryptionKey: instanceKey,
				ExitCode:      0,
			}
		} else {
			creator = nil
		}
		vaultService = wailsapi.NewVaultService(creator, errors.Join(creatorErr, instanceErr))
		workerExecutable, workerErr := bootstrap.SiblingWorkerExecutable()
		if workerErr == nil {
			executionFactory, executionInitializationError = bootstrap.NewDefaultExecutionFactory(root, workerExecutable)
		} else {
			executionInitializationError = workerErr
		}
	}
	executionService := wailsapi.NewExecutionService(vaultService, executionFactory, executionInitializationError)
	planService := wailsapi.NewPlanService(vaultService, workspaceService, bootstrap.NewEnvironmentModelFactory(executionFactory))
	reviewService := wailsapi.NewPatchReviewService(vaultService, executionFactory, executionInitializationError)
	patchService := wailsapi.NewPatchService(vaultService, executionFactory, executionInitializationError)
	var window *application.WebviewWindow
	if singleInstance != nil {
		singleInstance.OnSecondInstanceLaunch = func(application.SecondInstanceData) {
			if window != nil {
				window.Restore()
				window.Focus()
			}
		}
	}
	app := application.New(application.Options{
		Name:           "zdp-desktop-talos",
		Description:    "기억 기반 로컬 코딩 에이전트",
		SingleInstance: singleInstance,
		Services: []application.Service{
			application.NewService(&wailsapi.HealthService{}),
			application.NewService(vaultService),
			application.NewService(workspaceService),
			application.NewService(wailsapi.NewTaskService(vaultService, workspaceService)),
			application.NewService(wailsapi.NewDecisionService(vaultService, workspaceService)),
			application.NewService(wailsapi.NewMemoryService(vaultService, workspaceService)),
			application.NewService(wailsapi.NewProjectionService(vaultService)),
			application.NewService(planService),
			application.NewService(wailsapi.NewPermissionService(vaultService)),
			application.NewService(executionService),
			application.NewService(reviewService),
			application.NewService(patchService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	window = app.Window.NewWithOptions(application.WebviewWindowOptions{
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
