# Dockerfile
FROM golang:1.23-bookworm

RUN apt-get update && apt-get install -y \
    git \
    pkg-config \
    gcc \
    libgl1-mesa-dev \
    xorg-dev \
    gcc-mingw-w64 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Cache dependencies
COPY go.mod ./
RUN go mod download

# Copy source
COPY . .

# Build the server binary
RUN go build -o billder-server ./cmd/billder

# Run the server
CMD ["/app/billder-server"]