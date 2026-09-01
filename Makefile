.PHONY: build run clean icon package

icon:
	$(eval GOPATH := $(shell go env GOPATH))
	$(GOPATH)/bin/rsrc.exe -arch amd64 -ico icon.ico -o resource_windows_amd64.syso

build: icon
	go build -buildvcs=false -o smtui.exe .

run: build
	./smtui.exe

# Bundles smtui.exe + an example services.toml + README/LICENSE into
# dist/ServiceManagerTUI.zip for sharing with teammates.
package: build
	go run ./tools/packager

clean:
	rm -f smtui.exe resource_windows_amd64.syso
	rm -rf dist
