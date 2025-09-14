# Advanced Echo Server

[![Go Report Card](https://goreportcard.com/badge/github.com/arun0009/advanced-echo-server)](https://goreportcard.com/report/github.com/arun0009/advanced-echo-server)
[![Docker Pulls](https://img.shields.io/docker/pulls/arun0009/advanced-echo-server)](https://hub.docker.com/r/arun0009/advanced-echo-server)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/github/go-mod/go-version/arun0009/advanced-echo-server)](https://golang.org/)

> A powerful, feature-rich echo server designed for **API testing**, **load testing**, and **chaos engineering**

## Overview

Advanced Echo Server is a sophisticated testing tool that echoes back exactly what you send while providing extensive controls for simulating real-world conditions. Perfect for developers, DevOps engineers, and QA teams who need to test applications under various scenarios.

### Key Benefits

- **Precise Control**: Fine-tune server behavior via HTTP headers or environment variables
- **Chaos Engineering**: Inject failures, delays, and errors to test system resilience  
- **Load Testing**: Simulate high-latency responses and various error conditions
- **Developer Friendly**: Easy setup with Docker, comprehensive logging, and web interfaces
- **Production Ready**: HTTP/2 support, TLS, CORS, and security features built-in

## Table of Contents

- [Quick Start](#-quick-start)
- [Features](#-features)
- [Configuration](#-configuration)
- [Usage Examples](#-usage-examples)
- [Web Interface](#-web-interface)
- [Docker Usage](#-docker-usage)
- [API Reference](#-api-reference)
- [Contributing](#-contributing)
- [License](#-license)

## Quick Start

### Using Docker (Recommended)

```bash
# Pull and run the latest version
docker pull arun0009/advanced-echo-server:latest
docker run -p 8080:8080 arun0009/advanced-echo-server:latest

# Test it works
curl -X POST http://localhost:8080 -d '{"hello": "world"}'
```

### Using Go

```bash
# Clone and run
git clone https://github.com/arun0009/advanced-echo-server.git
cd advanced-echo-server
go run ./cmd/advanced-echo-server/main.go
```

### Using Docker Compose

```bash
# Clone repository
git clone https://github.com/arun0009/advanced-echo-server.git
cd advanced-echo-server

# Start with Docker Compose
docker-compose up -d
```

## Features

### Dynamic Control System

| Control Method | Usage | Priority |
|---|---|---|
| **HTTP Headers** | Per-request control | High |
| **Environment Variables** | Container-wide defaults | Low |

*Headers always override environment variables*

### Testing Capabilities

- **Delay Simulation**: Simple, jitter, random, and exponential delay patterns
- **Error Injection**: HTTP status codes, timeouts, and random failures  
- **Chaos Engineering**: Configurable failure rates and error scenarios
- **Response Control**: Dynamic body size, content type, and headers
- **Request Analytics**: Detailed request and server metadata

### Infrastructure Features

- **Security**: Automatic TLS certificate generation, CORS support
- **Modern Protocols**: HTTP/2 and H2C support
- **Containerized**: Multi-stage Docker builds for minimal image size
- **Web Interface**: Built-in WebSocket and SSE testing pages
- **Comprehensive Logging**: Request/response logging with configurable detail levels

## Configuration

### Environment Variables

| Variable | Description | Default | Example |
|---|---|---|---|
| `PORT` | Server listening port | `8080` | `PORT=9090` |
| `ENABLE_TLS` | Enable HTTPS | `false` | `ENABLE_TLS=true` |
| `CERT_FILE` | TLS certificate path | `server.crt` | `CERT_FILE=/certs/app.crt` |
| `KEY_FILE` | TLS private key path | `server.key` | `KEY_FILE=/certs/app.key` |
| `ENABLE_CORS` | Enable CORS headers | `true` | `ENABLE_CORS=false` |
| `LOG_REQUESTS` | Log request details | `true` | `LOG_REQUESTS=false` |
| `LOG_HEADERS` | Log all headers | `false` | `LOG_HEADERS=true` |
| `LOG_BODY` | Log request body | `false` | `LOG_BODY=true` |
| `MAX_BODY_SIZE` | Max request body size (bytes) | `10485760` | `MAX_BODY_SIZE=1048576` |

### Testing Controls

| Feature | HTTP Header | Environment Variable | Example Values |
|---|---|---|---|
| **Simple Delay** | `X-Echo-Delay` | `ECHO_DELAY` | `1000` (1 second) |
| **Random Delay** | `X-Echo-Random-Delay` | `ECHO_RANDOM_DELAY` | `100,500` (100-500ms) |
| **Force Status** | `X-Echo-Status` | `ECHO_STATUS` | `404`, `500`, `503` |
| **Simulate Error** | `X-Echo-Error` | `ECHO_ERROR` | `500`, `timeout`, `connection_reset` |
| **Chaos Rate** | `X-Echo-Chaos` | `ECHO_CHAOS` | `10` (10% failure rate) |
| **Custom Headers** | `X-Echo-Set-Header-*` | `ECHO_HEADER_*` | `ECHO_HEADER_X_Version=1.2.3` |

## Usage Examples

### Basic Echo Test

```bash
# Simple echo
curl -X POST http://localhost:8080 \
  -H "Content-Type: application/json" \
  -d '{"message": "Hello, World!"}'
```

### Load Testing Scenarios

```bash
# Simulate slow responses (2-second delay)
curl -X POST http://localhost:8080 \
  -H "X-Echo-Delay: 2000" \
  -d '{"test": "load testing"}'

# Random response times (100-500ms)
curl -X POST http://localhost:8080 \
  -H "X-Echo-Random-Delay: 100,500" \
  -d '{"test": "variable latency"}'
```

### Chaos Engineering

```bash
# 20% chance of random failures
curl -X POST http://localhost:8080 \
  -H "X-Echo-Chaos: 20" \
  -d '{"test": "chaos engineering"}'

# Force specific error
curl -X POST http://localhost:8080 \
  -H "X-Echo-Error: 503" \
  -d '{"test": "service unavailable"}'

# Simulate timeout
curl -X POST http://localhost:8080 \
  -H "X-Echo-Error: timeout" \
  -d '{"test": "timeout simulation"}'
```

### Response Customization

```bash
# Add custom response headers
curl -X POST http://localhost:8080 \
  -H "X-Echo-Set-Header-X-App-Version: 1.2.3" \
  -H "X-Echo-Set-Header-X-Environment: staging" \
  -d '{"test": "custom headers"}'

# Force specific status code
curl -X POST http://localhost:8080 \
  -H "X-Echo-Status: 201" \
  -d '{"status": "created"}'
```

## Web Interface

Access built-in testing interfaces:

- **WebSocket Client**: `http://localhost:8080/web-ws`
- **Server-Sent Events**: `http://localhost:8080/web-sse`

These interfaces provide interactive ways to test WebSocket connections and SSE streams directly from your browser.

## Docker Usage

### Basic Usage

```bash
# Run with default settings
docker run -p 8080:8080 arun0009/advanced-echo-server:latest

# Run with custom port
docker run -p 9090:9090 -e PORT=9090 arun0009/advanced-echo-server:latest
```

### Production Deployment

```bash
# Run with TLS and custom configuration
docker run -p 443:443 \
  -v /path/to/certs:/certs \
  -e PORT=443 \
  -e ENABLE_TLS=true \
  -e CERT_FILE=/certs/server.crt \
  -e KEY_FILE=/certs/server.key \
  -e LOG_REQUESTS=true \
  arun0009/advanced-echo-server:latest
```

### Docker Compose Example

```yaml
version: '3.8'
services:
  echo-server:
    image: arun0009/advanced-echo-server:latest
    ports:
      - "8080:8080"
    environment:
      - ECHO_DELAY=500
      - ECHO_CHAOS=5
      - LOG_REQUESTS=true
      - LOG_HEADERS=false
    volumes:
      - ./certs:/certs
```

## API Reference

### Endpoints

| Method | Path | Description |
|---|---|---|
| `ANY` | `/` | Main echo endpoint - accepts any HTTP method |
| `GET` | `/health` | Health check endpoint |
| `GET` | `/info` | Server information and configuration |
| `GET` | `/web-ws` | WebSocket testing interface |
| `GET` | `/web-sse` | Server-Sent Events testing interface |

## Contributing

We welcome contributions! Here's how you can help:

1. **Fork the repository**
2. **Create a feature branch**: `git checkout -b feature/amazing-feature`
3. **Commit your changes**: `git commit -m 'Add amazing feature'`
4. **Push to the branch**: `git push origin feature/amazing-feature`
5. **Open a Pull Request**

### Development Setup

```bash
# Clone your fork
git clone https://github.com/yourusername/advanced-echo-server.git
cd advanced-echo-server

# Install dependencies
go mod download

# Run tests
go test ./...

# Run with live reload (requires air)
air
```

### Reporting Issues

Please use the [GitHub Issues](https://github.com/arun0009/advanced-echo-server/issues) page to report bugs or request features. Include:

- Go version
- Operating system
- Detailed description of the issue
- Steps to reproduce

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.


## Stats

![GitHub stars](https://img.shields.io/github/stars/arun0009/advanced-echo-server?style=social)
![GitHub forks](https://img.shields.io/github/forks/arun0009/advanced-echo-server?style=social)
![GitHub issues](https://img.shields.io/github/issues/arun0009/advanced-echo-server)
![GitHub pull requests](https://img.shields.io/github/issues-pr/arun0009/advanced-echo-server)