APP_NAME := rtsp-virtual-cam-agent
REPO_ROOT := $(CURDIR)

.PHONY: build-linux build-windows package-linux package-windows clean

build-linux:
	go build -o $(APP_NAME) ./cmd/app

build-windows:
	GOOS=windows GOARCH=amd64 go build -o $(APP_NAME).exe ./cmd/app

package-linux:
	bash ./scripts/build-linux-appimage.sh

package-windows:
	pwsh -ExecutionPolicy Bypass -File ./scripts/build-windows.ps1

clean:
	rm -rf dist out AppDir $(APP_NAME) $(APP_NAME).exe
