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

package-windows:
	bash ./scripts/build-windows.sh

clean:
	rm -rf dist out AppDir $(APP_NAME) $(APP_NAME).exe
