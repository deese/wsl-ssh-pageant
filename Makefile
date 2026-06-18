BUILD_DIR := build
SRC_DIR   := src
GOARCH    := amd64
GOOS      := windows

BIN       := $(BUILD_DIR)/wsl-ssh-pageant-$(GOARCH).exe
BIN_GUI   := $(BUILD_DIR)/wsl-ssh-pageant-$(GOARCH)-gui.exe
ASSETS    := $(SRC_DIR)/assets.go

.PHONY: all clean generate

all: $(BIN) $(BIN_GUI)

generate: $(ASSETS)

$(ASSETS): $(SRC_DIR)/assets/icon.ico
	cd $(SRC_DIR) && go run github.com/go-bindata/go-bindata/go-bindata -pkg main -o assets.go assets/

$(BIN): $(ASSETS)
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && GOARCH=$(GOARCH) GOOS=$(GOOS) go build -o ../$(BIN) .

$(BIN_GUI): $(ASSETS)
	@mkdir -p $(BUILD_DIR)
	cd $(SRC_DIR) && GOARCH=$(GOARCH) GOOS=$(GOOS) go build -ldflags "-H=windowsgui" -o ../$(BIN_GUI) .

clean:
	rm -rf $(BUILD_DIR) $(ASSETS)
