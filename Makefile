proto:
	protoc --go_out=. --go-grpc_out=. example.proto

clean:	
	rm -rf example
	rm -rf go.mod
	rm -rf go.sum
	go clean -modcache

runl:	
	go mod tidy
	go run lester.go

runf:
	go mod tidy
	go run franklin.go

runt:
	go mod tidy
	go run trevor.go

runclient:
	go run michael.go
	
init:
	export PATH="$PATH:$(go env GOPATH)/bin"
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	echo $$PATH
	go mod init example		
	