.PHONY: test-ec test-fintech test-saas test-social test-travel test-nestkey test-multikey test-provides test-all
.PHONY: partial-test-ec partial-test-fintech partial-test-saas partial-test-social partial-test-travel partial-test-all
.PHONY: setup benchmark benchmark-ec benchmark-fintech benchmark-saas benchmark-social benchmark-travel compare-benchmark all-compare-benchmark
.PHONY: run stop

# ── Per-domain service up/down ────────────────────────────────────────────────
# Usage:
#   make run <domain>        # e.g. make run ec
#   make run-<domain>        # e.g. make run-ec
#   make stop <domain>       # bring services down
#   make run <domain> DETACH=1   # start detached (-d)
#
# Supported domains: ec, fintech, saas, social, travel, nestkey, multikey, provides
DOMAINS := ec fintech saas social travel nestkey multikey provides

# `make run ec` の 2 番目の引数をダミーターゲットとして受け流す。
# (何も実行しないが、make が未知ターゲット扱いでエラーを出すのを防ぐ。)
$(DOMAINS):
	@:

# run / stop が goal に含まれるときだけ、続く引数をドメイン名として取り出す。
ifneq ($(filter run stop,$(MAKECMDGOALS)),)
RUN_DOMAIN := $(firstword $(filter $(DOMAINS),$(MAKECMDGOALS)))
endif

DETACH ?=
COMPOSE_UP_FLAGS := --build
ifeq ($(DETACH),1)
COMPOSE_UP_FLAGS += -d
endif

run:
	@if [ -z "$(RUN_DOMAIN)" ]; then \
		echo "usage: make run <domain>   (domains: $(DOMAINS))"; \
		exit 2; \
	fi
	@echo "==> starting $(RUN_DOMAIN) services"
	cd $(RUN_DOMAIN) && docker compose -f docker-compose.yaml up $(COMPOSE_UP_FLAGS)

stop:
	@if [ -z "$(RUN_DOMAIN)" ]; then \
		echo "usage: make stop <domain>   (domains: $(DOMAINS))"; \
		exit 2; \
	fi
	@echo "==> stopping $(RUN_DOMAIN) services"
	cd $(RUN_DOMAIN) && docker compose -f docker-compose.yaml down

# 便利な `make run-ec` スタイルのパターンターゲットも提供する。
run-%:
	@if ! echo "$(DOMAINS)" | tr ' ' '\n' | grep -qx "$*"; then \
		echo "unknown domain: $*   (domains: $(DOMAINS))"; \
		exit 2; \
	fi
	@echo "==> starting $* services"
	cd $* && docker compose -f docker-compose.yaml up $(COMPOSE_UP_FLAGS)

stop-%:
	@if ! echo "$(DOMAINS)" | tr ' ' '\n' | grep -qx "$*"; then \
		echo "unknown domain: $*   (domains: $(DOMAINS))"; \
		exit 2; \
	fi
	@echo "==> stopping $* services"
	cd $* && docker compose -f docker-compose.yaml down
# ──────────────────────────────────────────────────────────────────────────────

# Setup benchmark dependencies (hey, Docker check, gateway binary build)
setup:
	bash setup_benchmark.sh

# ── Benchmark targets ──────────────────────────────────────────────────────────
# Go Gateway vs Apollo Router — identical conditions:
#   • Both run in Docker (same execution model)
#   • Resource limits: 2 CPU / 512 MB each (deploy.resources.limits in docker-compose.*.yaml)
#   • Warmup: WARMUP_REQUESTS=100 requests at WARMUP_CONCURRENCY=10 (same for both)
#   • Measurement: TOTAL_REQUESTS=10000 at CONCURRENCY=50
# Tunable via environment variables, e.g.:
#   TOTAL_REQUESTS=5000 CONCURRENCY=20 make benchmark

# All domains (default benchmark target)
benchmark:
	bash all_compare_benchmark_runner.sh

# Single-domain benchmarks
benchmark-ec:
	bash domain_benchmark.sh ec 9001 \
		'{"query":"query ProductWithShipping { product(id: \"p1\") { id name price reviews { id body authorName } inStock shippingCost } }"}' \
		"EC - @external + @requires (Cross-Service)"

benchmark-fintech:
	bash domain_benchmark.sh fintech 9002 \
		'{"query":"query CustomerWithAccounts { customer(id: \"1\") { id name tier accounts { iban balance riskScore } } }"}' \
		"Fintech - @external + @requires (Risk Score)"

benchmark-saas:
	bash domain_benchmark.sh saas 9003 \
		'{"query":"query OrganizationWithBilling { organization(id: \"org1\") { id name employeeCount billing { plan } monthlyCost } }"}' \
		"SaaS - @external + @requires (Monthly Cost)"

benchmark-social:
	bash domain_benchmark.sh social 9004 \
		'{"query":"query UserWithPosts { user(id: \"user1\") { id name posts { id title likeCount comments { body } engagementScore } } }"}' \
		"Social - @external + @requires (Engagement)"

benchmark-travel:
	bash domain_benchmark.sh travel 9005 \
		'{"query":"query FlightWithBookings { flight(number: \"AA100\", departureDate: \"2026-03-01\") { number origin destination price bookings { id } totalCost } }"}' \
		"Travel - @external + @requires (Total Cost)"

# Legacy aliases (kept for backward compatibility)
compare-benchmark:
	bash compare_benchmark_runner.sh

all-compare-benchmark: benchmark
# ──────────────────────────────────────────────────────────────────────────────

# Success test targets (normal operation)
test-ec:
	bash success_test_runner.sh ec

test-fintech:
	bash success_test_runner.sh fintech

test-saas:
	bash success_test_runner.sh saas

test-social:
	bash success_test_runner.sh social

test-travel:
	bash success_test_runner.sh travel

test-nestkey:
	bash success_test_runner.sh nestkey

test-multikey:
	bash success_test_runner.sh multikey

test-provides:
	bash success_test_runner.sh provides

test-all: test-ec test-fintech test-saas test-social test-travel test-nestkey test-multikey test-provides

# Partial response test targets (service failure scenarios)
partial-test-ec:
	bash partial_test_runner.sh ec

partial-test-fintech:
	bash partial_test_runner.sh fintech

partial-test-saas:
	bash partial_test_runner.sh saas

partial-test-social:
	bash partial_test_runner.sh social

partial-test-travel:
	bash partial_test_runner.sh travel

partial-test-all: partial-test-ec partial-test-fintech partial-test-saas partial-test-social partial-test-travel