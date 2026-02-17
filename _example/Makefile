.PHONY: test-ec test-fintech test-saas test-social test-travel test-all
.PHONY: partial-test-ec partial-test-fintech partial-test-saas partial-test-social partial-test-travel partial-test-all

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