APP_NAME := nystavision
REPO_ROOT := $(CURDIR)

.PHONY: build-linux build-mac build-windows package-linux package-windows clean run run-distrobox build-distrobox

build-mac:
	bash ./scripts/build-mac.sh

build-linux:
	go build -ldflags="-X 'nystavision/internal/version.Version=${VERSION}'" -o $(APP_NAME) ./cmd/app

run:
	go run ./cmd/app


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
