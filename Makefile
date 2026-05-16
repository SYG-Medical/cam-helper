APP_NAME := rtsp-virtual-cam-agent
REPO_ROOT := $(CURDIR)

.PHONY: build-linux build-windows package-linux package-windows clean

build-linux:
	go build -o $(APP_NAME) ./cmd/app

build-windows:
	bash ./scripts/build-windows.sh

build-distrobox-windows:
	distrobox enter dev-backend -- make build-windows

package-linux:
	bash ./scripts/build-linux-appimage.sh

fetch-deps:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./scripts/fetch-deps.ps1

package-windows:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File ./scripts/build-windows.ps1

clean:
	rm -rf dist out AppDir $(APP_NAME) $(APP_NAME).exe
