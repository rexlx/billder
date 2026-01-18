# Dockerfile
FROM golang:1.23-bookworm

# Install dependencies (Git, MinGW for Windows, X11 for Linux GUI)
RUN apt-get update && apt-get install -y \
    git \
    pkg-config \
    gcc \
    libgl1-mesa-dev \
    xorg-dev \
    gcc-mingw-w64 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy module files first for caching
COPY go.mod ./
# COPY go.sum ./
RUN go mod download

# Copy the entire source tree (cmd, internal, pkg, etc.)
COPY . .

# --- CHANGED SECTION ---
# Build the specific binary located in cmd/billder
# We output it as 'billder-server' just to be explicit
RUN go build -o billder-server ./cmd/billder

# Run the binary
CMD ["/app/billder-server"]