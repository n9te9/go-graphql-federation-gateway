#!/bin/bash

# Setup script for benchmark tools and dependencies
# Installs hey, checks Docker, and verifies Apollo Router access

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}=== Benchmark Tools Setup ===${NC}"
echo ""

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# 1. Check/Install hey
echo -e "${YELLOW}[1/4] Checking hey (HTTP load testing tool)...${NC}"
if command_exists hey; then
    HEY_VERSION=$(hey -version 2>&1 | head -1 || echo "unknown")
    echo -e "${GREEN}✓ hey is already installed: ${HEY_VERSION}${NC}"
else
    echo -e "${YELLOW}hey is not installed. Installing...${NC}"
    
    # Detect OS
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        if command_exists brew; then
            brew install hey
            echo -e "${GREEN}✓ hey installed successfully via Homebrew${NC}"
        else
            echo -e "${RED}✗ Homebrew not found. Please install Homebrew first:${NC}"
            echo "  /bin/bash -c \"\$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)\""
            exit 1
        fi
    elif [[ "$OSTYPE" == "linux-gnu"* ]]; then
        # Linux
        echo -e "${YELLOW}Installing hey from GitHub releases...${NC}"
        curl -sfL https://hey-release.s3.us-east-2.amazonaws.com/hey_linux_amd64 -o /tmp/hey
        chmod +x /tmp/hey
        sudo mv /tmp/hey /usr/local/bin/hey
        echo -e "${GREEN}✓ hey installed successfully${NC}"
    else
        echo -e "${RED}✗ Unsupported OS: $OSTYPE${NC}"
        echo "  Please install hey manually from: https://github.com/rakyll/hey"
        exit 1
    fi
fi
echo ""

# 2. Check Docker
echo -e "${YELLOW}[2/4] Checking Docker...${NC}"
if command_exists docker; then
    DOCKER_VERSION=$(docker --version)
    echo -e "${GREEN}✓ Docker is installed: ${DOCKER_VERSION}${NC}"
    
    # Check if Docker daemon is running
    if docker ps >/dev/null 2>&1; then
        echo -e "${GREEN}✓ Docker daemon is running${NC}"
    else
        echo -e "${RED}✗ Docker daemon is not running${NC}"
        echo "  Please start Docker Desktop or Docker daemon"
        exit 1
    fi
else
    echo -e "${RED}✗ Docker is not installed${NC}"
    echo "  Please install Docker from: https://docs.docker.com/get-docker/"
    exit 1
fi
echo ""

# 3. Check Docker Compose
echo -e "${YELLOW}[3/4] Checking Docker Compose...${NC}"
if docker compose version >/dev/null 2>&1; then
    COMPOSE_VERSION=$(docker compose version)
    echo -e "${GREEN}✓ Docker Compose is available: ${COMPOSE_VERSION}${NC}"
else
    echo -e "${RED}✗ Docker Compose is not available${NC}"
    echo "  Please install Docker Compose plugin"
    exit 1
fi
echo ""

# 4. Check Apollo Router image availability
echo -e "${YELLOW}[4/4] Checking Apollo Router Docker image...${NC}"
APOLLO_IMAGE="ghcr.io/apollographql/router:v1.52.0"

# Try to pull the image
if docker image inspect "$APOLLO_IMAGE" >/dev/null 2>&1; then
    echo -e "${GREEN}✓ Apollo Router image is available locally${NC}"
else
    echo -e "${YELLOW}Pulling Apollo Router image...${NC}"
    if docker pull "$APOLLO_IMAGE"; then
        echo -e "${GREEN}✓ Apollo Router image pulled successfully${NC}"
    else
        echo -e "${RED}✗ Failed to pull Apollo Router image${NC}"
        echo "  Image: $APOLLO_IMAGE"
        exit 1
    fi
fi
echo ""

# 5. Check Go installation (for building gateway)
echo -e "${YELLOW}[5/5] Checking Go installation...${NC}"
if command_exists go; then
    GO_VERSION=$(go version)
    echo -e "${GREEN}✓ Go is installed: ${GO_VERSION}${NC}"
    
    # Build gateway binary if not exists
    cd ..
    if [ ! -f "cmd/go-graphql-federation-gateway/gateway" ]; then
        echo -e "${YELLOW}Building gateway binary...${NC}"
        go build -o cmd/go-graphql-federation-gateway/gateway cmd/go-graphql-federation-gateway/main.go
        echo -e "${GREEN}✓ Gateway binary built successfully${NC}"
    else
        echo -e "${GREEN}✓ Gateway binary already exists${NC}"
    fi
    cd _example
else
    echo -e "${RED}✗ Go is not installed${NC}"
    echo "  Please install Go from: https://go.dev/doc/install"
    exit 1
fi
echo ""

# Summary
echo -e "${BLUE}=== Setup Complete ===${NC}"
echo ""
echo -e "${GREEN}All required tools are installed and ready!${NC}"
echo ""
echo "You can now run benchmarks:"
echo "  ${CYAN}make benchmark${NC}              - Benchmark all domains with Go Gateway"
echo "  ${CYAN}make compare-benchmark${NC}      - Compare Go Gateway vs Apollo Router (EC domain)"
echo "  ${CYAN}make all-compare-benchmark${NC}  - Compare across all domains"
echo ""
