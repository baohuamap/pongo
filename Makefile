include ./config.mk

init:
	ln -s hooks/pre-commit .git/hooks/pre-commit
	ln -s hooks/pre-push .git/hooks/pre-push
	$(eval GOPATH := $(shell go env GOPATH))
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh | sh -s -- -b $(GOPATH)/bin ${LINTER_VERSION}