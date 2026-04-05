# Stage 1: Build the ae-agent binary
FROM golang:1.25 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /ae-agent ./cmd/ae-agent

# Stage 2: Runtime image
FROM ubuntu:22.04

ENV DEBIAN_FRONTEND=noninteractive

# Install system dependencies + Node.js 20
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    git \
    jq \
    wget \
    gnupg \
    unzip \
    ripgrep \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install Playwright + Chromium
RUN npx -y playwright install --with-deps chromium

# Install Google Workspace CLI
RUN ARCH=$(dpkg --print-architecture) && \
    curl -sL "https://github.com/googleworkspace/cli/releases/latest/download/gws_linux_${ARCH}" -o /usr/local/bin/gws && \
    chmod +x /usr/local/bin/gws || echo "Google Workspace CLI not available for this arch"

# Create non-root user
RUN useradd -m -s /bin/bash -u 1000 ae

# Create workspace and scratch directories
RUN mkdir -p /workspace /home/ae/scratch && \
    chown -R ae:ae /workspace /home/ae/scratch

# Copy the agent binary
COPY --from=builder /ae-agent /usr/local/bin/ae-agent

USER ae
WORKDIR /workspace

EXPOSE 9090

ENTRYPOINT ["/usr/local/bin/ae-agent"]
