# Build stage
FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-X main.Version=$(git describe --tags --always --dirty 2>/dev/null || echo docker)" \
    -o /factory ./cmd/factory/

# Runtime stage — needs Go + Node for pipeline check commands
FROM golang:1.25-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
    tmux git curl ca-certificates gnupg docker.io make \
    && rm -rf /var/lib/apt/lists/*

# Install gh CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
    | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
    > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y gh && rm -rf /var/lib/apt/lists/*

# Install Node.js (for claude CLI and frontend projects)
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*

# Install Claude CLI
RUN npm install -g @anthropic-ai/claude-code

# Copy binary and entrypoint
COPY --from=builder /factory /usr/local/bin/factory
COPY deploy/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create non-root user
RUN useradd -m -s /bin/bash factory
USER factory
WORKDIR /home/factory

# Data directory
ENV FACTORY_DATA_DIR=/data
VOLUME ["/data"]

EXPOSE 17432

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
