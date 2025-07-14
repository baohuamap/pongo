LINTER_VERSION ?= v2.2.2
BUF_VERSION ?= v1.50.0
SWAGGER_UI_VERSION:=v4.15.5

init:
	ln -sf hooks/pre-commit .git/hooks/pre-commit
	ln -sf hooks/pre-push .git/hooks/pre-push
	$(eval GOPATH := $(shell go env GOPATH))
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(GOPATH)/bin ${LINTER_VERSION}

generate: generate/proto generate/swagger-ui
generate/proto:
	go run github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION) generate
generate/swagger-ui:
	SWAGGER_UI_VERSION=$(SWAGGER_UI_VERSION) ./scripts/generate-swagger-ui.sh

