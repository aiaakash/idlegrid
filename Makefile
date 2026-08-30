.PHONY: build test fmt run-coordinator run-fake run-provider stop clean

build:
	go build -o .build/coordinator ./coordinator/cmd/coordinator
	go build -o .build/fakeprovider ./coordinator/cmd/fakeprovider
	cd provider-swift && ./build.sh

test:
	go test ./...

fmt:
	gofmt -w .

run-coordinator:
	go run ./coordinator/cmd/coordinator

# Simulated fleet for load testing: make run-fake N=100
run-fake:
	go run ./coordinator/cmd/fakeprovider -count $(or $(N),8)

# Real provider on this Mac. Default model downloads from Hugging Face on
# first run. Override with: make run-provider MODEL=mlx-community/Some-Model
run-provider:
	cd provider-swift && .build/release/idlegrid-provider \
		--coordinator $(or $(COORD),ws://localhost:8090/ws/provider) \
		--model $(or $(MODEL),mlx-community/Qwen2.5-0.5B-Instruct-4bit) \
		$(if $(NAME),--name $(NAME),)

# Kill anything holding the coordinator port (stray coordinators/providers).
stop:
	lsof -ti :$(or $(PORT),8090) | xargs kill -9 2>/dev/null; \
	pkill -9 -f fakeprovider 2>/dev/null; \
	pkill -9 -f idlegrid-provider 2>/dev/null; \
	pkill -9 -f "llama-server.*--port 8199" 2>/dev/null; \
	pkill -9 -f "llama-server.*--port 8200" 2>/dev/null; \
	echo "stopped (coordinator, providers, llama-servers)"

clean:
	rm -rf .build provider-swift/.build
