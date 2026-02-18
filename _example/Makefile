.PHONY: test-ec test-fintech test-saas test-social test-travel test-all
.PHONY: partial-test-ec partial-test-fintech partial-test-saas partial-test-social partial-test-travel partial-test-all
.PHONY: setup benchmark compare-benchmark all-compare-benchmark

# Setup benchmark dependencies (hey, Apollo Router, gateway binary)
setup:
	bash setup_benchmark.sh

# Benchmark target (Go Gateway only, all domains)
benchmark:
	bash benchmark_runner.sh

# Comparative benchmark target (Go Gateway vs Apollo Router - EC domain only)
compare-benchmark:
	bash compare_benchmark_runner.sh

# Comprehensive comparative benchmark (Go Gateway vs Apollo Router - ALL domains)
all-compare-benchmark:
	bash all_compare_benchmark_runner.sh

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

test-all: test-ec test-fintech test-saas test-social test-travel

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