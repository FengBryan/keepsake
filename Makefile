ENVIRONMENT := development

OS := $(shell uname -s)

.PHONY: build
build: verify-dev-env
	cd golang && $(MAKE) build-all ENVIRONMENT=$(ENVIRONMENT)
	cd python && $(MAKE) build

.PHONY: install
install: build
ifeq ($(OS),Linux)
	pip install python/dist/keepsake-*-py3-none-manylinux1_x86_64.whl
else ifeq ($(OS),Darwin)
	pip install python/dist/keepsake-*-py3-none-macosx_*.whl
else
	@echo Unknown OS: $(OS)
endif

.PHONY: develop
develop: verify-dev-env
	cd golang && $(MAKE) build
	cd golang && $(MAKE) install
	cd python && python setup.py develop

.PHONY: install-test-dependencies
install-test-dependencies:
	pip install -r requirements-test.txt

.PHONY: test
test: install-test-dependencies develop
	cd golang && $(MAKE) test
	cd python && $(MAKE) test
	cd end-to-end-test && $(MAKE) test

.PHONY: test-external
test-external: install-test-dependencies develop
	cd golang && $(MAKE) test-external
	cd python && $(MAKE) test-external
	cd end-to-end-test && $(MAKE) test-external

.PHONY: release
release: check-version-var verify-clean-main bump-version
	git add golang/Makefile python/keepsake/version.py web/.env
	git commit -m "Bump to version $(VERSION)"
	git tag "v$(VERSION)"
	git push git@github.com:replicate/keepsake.git main
	git push git@github.com:replicate/keepsake.git main --tags
	git push git@github.com:replicate/keepsake.git main:website --force

.PHONY: verify-version
# quick and dirty
bump-version:
	sed -E -i '' "s/VERSION := .+/VERSION := $(VERSION)/" golang/Makefile
	sed -E -i '' 's/version = ".+"/version = "$(VERSION)"/' python/keepsake/version.py
	sed -E -i '' 's/NEXT_PUBLIC_VERSION=.+/NEXT_PUBLIC_VERSION=$(VERSION)/' web/.env

.PHONY: verify-clean-main
verify-clean-main:
	git diff-index --quiet HEAD  # make sure git is clean
	git checkout main
	git pull git@github.com:replicate/keepsake.git main

.PHONY: release-manual
release-manual: check-version-var verify-clean-main
	cd golang && $(MAKE) build-all ENVIRONMENT=production
	cd python && $(MAKE) build
	cd python && twine check dist/*
	cd python && twine upload dist/*

.PHONY: check-version-var
check-version-var:
	test $(VERSION)

.PHONY: verify-dev-env
verify-dev-env: verify-go-version verify-python-version

.PHONY: verify-go-version
verify-go-version:
	@./makefile-scripts/verify-go-version.sh

.PHONY: verify-python-version
verify-python-version:
	@./makefile-scripts/verify-python-version.sh

.PHONY: fmt
fmt:
	cd golang && $(MAKE) fmt
	cd python && $(MAKE) fmt
