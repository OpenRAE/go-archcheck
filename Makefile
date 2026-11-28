.DEFAULT_GOAL := help
.PHONY: help tools hygiene lint security test mutation fuzz regression arch prose config ci-check

MISE ?= mise
GO = $(MISE) exec -- go
GOLANGCI = .cache/bin/golangci-lint
RAESVET = .cache/bin/raesvet
GOVULNCHECK = .cache/bin/govulncheck
COVERAGE = .cache/bin/go-test-coverage
REGRESSIONTEST = .cache/bin/regressiontest
GREMLINS = .cache/bin/gremlins

# The doc-tools release gives the STE checks, the DocTools Vale style and the documentation skill.
# Change the version and the SHA-256 of the release archive together. The release SHA256SUMS file
# gives the SHA-256.
DOC_TOOLS_VERSION = 0.1.0
DOC_TOOLS_SHA256 = 125104c0cc535698d1cc9bba3e57883bdf9d87db6da346ff583967c4ce42de6c
DOC_TOOLS = .cache/doc-tools/$(DOC_TOOLS_VERSION)
DOC_TOOLS_URL = https://github.com/OpenRAE/doc-tools/releases/download/v$(DOC_TOOLS_VERSION)/doc-tools-$(DOC_TOOLS_VERSION).tar.gz

# For a local check, give the changed files in PROSE_PATHS.
ifeq ($(origin PROSE_PATHS),undefined)
PROSE_INPUT = git ls-files -z -- '*.md' ':(glob,exclude)**/testdata/**' | xargs -0
endif

help:
	@echo 'Checks: make lint, make security, make test, make mutation, make fuzz, make regression, make arch, make prose, make config, make hygiene'
	@echo 'All checks, as CI runs them: make ci-check'

tools:
	$(MISE) install
	$(MISE) exec -- uv sync --locked --no-build

# File hygiene, workflow lint, workflow security and secret detection.
hygiene:
	$(MISE) exec -- uv run --locked --no-build pre-commit run --all-files

# The tools module pins the Go tools, so the checker module never depends on them.
$(GOLANGCI): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ github.com/golangci/golangci-lint/v2/cmd/golangci-lint

$(RAESVET): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ github.com/OpenRAE/rae/implementations/tooling/go/cmd/raesvet

$(GOVULNCHECK): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ golang.org/x/vuln/cmd/govulncheck

$(COVERAGE): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ github.com/vladopajic/go-test-coverage/v2

$(REGRESSIONTEST): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ github.com/OpenRAE/rae/implementations/tooling/go/cmd/regressiontest

$(GREMLINS): tools/go.mod tools/go.sum
	$(GO) build -modfile tools/go.mod -o $@ github.com/OpenRAE/go-rae-gremlins/cmd/gremlins

# The lint gate: golangci-lint, go vet, the OpenRÆ raesvet analyzers, and tidy and fix checks.
lint: $(GOLANGCI) $(RAESVET)
	$(GO) mod tidy -diff
	cd tools && $(GO) mod tidy -diff
	$(GOLANGCI) run ./...
	$(GO) vet ./...
	$(GO) vet -vettool=$(CURDIR)/$(RAESVET) ./...
	$(GO) fix -diff ./...

# The dependency gate: module verification, known vulnerabilities and the license scan, which
# fails on restricted, forbidden and unknown licenses. The tools module never ships.
security: $(GOVULNCHECK)
	$(GO) mod verify
	$(GO) mod download
	$(MISE) exec -- trivy fs --scanners license --severity UNKNOWN,HIGH,CRITICAL --exit-code 1 --quiet \
		--skip-dirs tools --skip-dirs .venv --skip-dirs .cache --skip-files uv.lock .
	$(GOVULNCHECK) ./...

# The tests, with the race detector, shuffled order and at least 90% coverage for each package.
test: $(COVERAGE)
	mkdir -p .cache
	$(GO) test -race -shuffle=on -covermode=atomic -coverprofile=.cache/coverage.out ./...
	$(COVERAGE) --config .testcoverage.yml

# Mutation testing fails when the test efficacy or the mutant coverage is below 90%.
mutation: $(GREMLINS)
	$(MISE) exec -- $(CURDIR)/$(GREMLINS) unleash --threshold-efficacy 90 --threshold-mcover 90 --output-statuses lct

# Each fuzz target of the parsers runs for a short time. A failing input lands in testdata/fuzz,
# and the fix commits it as a regression seed.
fuzz:
	$(GO) test -run='^$$' -fuzz='^FuzzParse$$' -fuzztime=15s ./internal/model
	$(GO) test -run='^$$' -fuzz='^FuzzParseFrontMatter$$' -fuzztime=15s ./internal/record
	$(GO) test -run='^$$' -fuzz='^FuzzParseReservations$$' -fuzztime=15s ./internal/record

# BASE_REV is the git revision of the base, for example origin/dev or a commit.
# Each new TestRegression test in a *_regression_test.go file must fail on the base revision.
regression: $(REGRESSIONTEST)
	$(MISE) exec -- $(CURDIR)/$(REGRESSIONTEST) -base "$${BASE_REV:-origin/dev}"

# The checker checks its own model, records and code. With a base revision, a structural change
# of the model needs a new design record. Each design record needs its reservation tag in origin.
# The fetch makes the local design/NNNN tags the same as the tags in origin.
arch:
	@mkdir -p .cache
	git fetch --quiet --prune origin '+refs/tags/design/*:refs/tags/design/*'
	git for-each-ref --format='%(refname)%09%(objecttype)%09%(contents:subject)' refs/tags/design/ > .cache/reservations.txt
	@if [ -n "$$BASE_REV" ] && git cat-file -e "$$BASE_REV:architecture/model.yaml" 2>/dev/null; then \
		git show "$$BASE_REV:architecture/model.yaml" > .cache/base-model.yaml; \
		git ls-tree --name-only "$$BASE_REV" architecture/designs/ > .cache/base-designs.txt; \
		$(GO) run ./cmd/archcheck -reservations .cache/reservations.txt -base-model .cache/base-model.yaml -base-designs .cache/base-designs.txt; \
	else $(GO) run ./cmd/archcheck -reservations .cache/reservations.txt; fi

# The download stops before extraction when the SHA-256 is not the recorded value.
# The ste-cli installer of the package verifies the ste-cli archive in the same way.
$(DOC_TOOLS)/.verified:
	rm -rf $(DOC_TOOLS) $(DOC_TOOLS).tar.gz
	mkdir -p $(DOC_TOOLS)
	curl --proto '=https' --fail --silent --show-error --location -o $(DOC_TOOLS).tar.gz $(DOC_TOOLS_URL)
	echo '$(DOC_TOOLS_SHA256)  $(DOC_TOOLS).tar.gz' | sha256sum -c -
	tar -xzf $(DOC_TOOLS).tar.gz -C $(DOC_TOOLS) --strip-components=1
	bash $(DOC_TOOLS)/tools/install-ste.sh
	rm -rf styles/DocTools
	cp -R $(DOC_TOOLS)/vale/DocTools styles/DocTools
	touch $@

# The documentation follows ASD-STE100. Vale applies the STE and DocTools styles, and
# ste-cli applies the doc-tools STE rules.
prose: private SHELL := /bin/bash
prose: private .SHELLFLAGS := -o pipefail -c
prose: $(DOC_TOOLS)/.verified
	$(PROSE_INPUT) $(MISE) exec -- vale -- $(PROSE_PATHS)
	$(PROSE_INPUT) bash $(DOC_TOOLS)/tools/ste-lint.sh -- $(PROSE_PATHS)

# The configuration check and the tests of the repository scripts.
config:
	mkdir -p .cache
	$(MISE) exec -- uv run --locked --no-build python scripts/check_config.py
	$(MISE) exec -- uv run --locked --no-build coverage run --source=scripts -m unittest discover -s tests
	$(MISE) exec -- uv run --locked --no-build coverage xml -o .cache/python-coverage.xml

ci-check: lint security test mutation fuzz regression arch prose config
