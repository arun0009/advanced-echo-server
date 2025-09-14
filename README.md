# Advanced Echo Server

A powerful echo server designed for **API testing, load testing, and chaos engineering**. It echoes back exactly what you send, with behavior dynamically controlled by **HTTP headers or environment variables**.

### Core Principle

This server echoes back your request body exactly as received. Its advanced testing features are controlled via special HTTP headers, which can be **overridden by environment variables** for consistent, container-level behavior.

---

### Key Features

#### Dynamic Control

* **Header-Controlled:** Set features on a per-request basis with HTTP headers (e.g., `X-Echo-Delay: 1000`).
* **Environment Variable-Controlled:** Apply consistent behavior across all requests by setting environment variables (e.g., `ECHO_DELAY=1000`).

#### Advanced Testing

* **Delay Simulation:** Fine-tune delays with simple, jitter, random, and exponential patterns.
* **Error & Chaos Injection:** Force specific HTTP status codes, simulate common errors (`500`, `429`, `timeout`), or inject random failures.
* **Dynamic Response Generation:** Control response body size, content type, and headers dynamically.
* **Request & Server Info:** Return detailed metadata about the request and the server serving it.

#### Production & Development Ready

* **HTTP/2 & H2C Support:** Seamlessly deploy behind modern proxies and load balancers.
* **Docker Ready:** A multi-stage `Dockerfile` provides a small, secure image.
* **Docker Compose:** A `docker-compose.yml` for quick local setup and testing.
* **Embedded Web Frontend:** Simple web pages for testing WebSocket and SSE from your browser.
* **Automatic TLS:** Generates a self-signed certificate for HTTPS if one isn't provided.

---

### Quick Start

#### Run with Go

```bash
# Clone the repository
git clone [https://github.com/arun0009/advanced-echo-server.git](https://github.com/arun0009/advanced-echo-server.git)
cd advanced-echo-server

# To run a basic HTTP server
go run ./cmd/advanced-echo-server/main.go

# To run with a global 5% chaos rate
ECHO_CHAOS=5 go run ./cmd/advanced-echo-server/main.go
```

# Run with Docker

The easiest way to get started is to pull the pre-built image from Docker Hub.

```bash
# Pull the latest image
docker pull arun0009/advanced-echo-server:latest

# Run with a 1-second delay and a custom header
docker run -p 8080:8080 \
  -e ECHO_DELAY=1000 \
  -e ECHO_HEADER_X_Trace_ID=abc-123 \
  arun0009/advanced-echo-server:latest
```

# Configuration & Usage

All testing features can be controlled by either a specific HTTP header or its corresponding environment variable. Headers take precedence over environment variables.

### Configuration via Environment Variables
The server's behavior can be customized by setting the following environment variables. If a variable is not set, the server will use its sane default value.

| Variable      | Description                                      | Default Value         | Example Usage                |
|--------------|--------------------------------------------------|----------------------|------------------------------|
| PORT         | The port the server listens on.                   | 8080                 | PORT=9090                    |
| ENABLE_TLS   | Enables HTTPS. Requires CERT_FILE and KEY_FILE.   | false                | ENABLE_TLS=true              |
| CERT_FILE    | Path to the TLS certificate file.                 | server.crt           | CERT_FILE=/path/to/cert.pem  |
| KEY_FILE     | Path to the TLS private key file.                 | server.key           | KEY_FILE=/path/to/key.pem    |
| ENABLE_CORS  | Enables a permissive CORS policy.                 | true                 | ENABLE_CORS=false            |
| LOG_REQUESTS | Logs basic request details and timings.           | true                 | LOG_REQUESTS=false           |
| LOG_HEADERS  | Logs all request headers to the console.          | false                | LOG_HEADERS=true             |
| LOG_BODY     | Logs the request body to the console.             | false                | LOG_BODY=true                |
| MAX_BODY_SIZE| The maximum size of the request body in bytes.    | 10485760 (10MB)      | MAX_BODY_SIZE=1048576        |

# Chaos Engineering

All testing features can be controlled by either a specific HTTP header or its corresponding environment variable. Headers take precedence over environment variables.

| Feature         | HTTP Header           | Environment Variable | Example                          |
|-----------------|-----------------------|---------------------|----------------------------------|
| Simple Delay    | X-Echo-Delay          | ECHO_DELAY          | 1000 (1 second)                  |
| Random Delay    | X-Echo-Random-Delay   | ECHO_RANDOM_DELAY   | 100,500 (100-500ms)              |
| Force Status    | X-Echo-Status         | ECHO_STATUS         | 404                              |
| Simulate Error  | X-Echo-Error          | ECHO_ERROR          | 500 or timeout                   |
| Chaos Rate      | X-Echo-Chaos          | ECHO_CHAOS          | 10 (10% rate)                    |
| Set Header      | X-Echo-Set-Header-*   | ECHO_HEADER_*       | ECHO_HEADER_X_App_Version=1.0    |

# Example: Load Testing with Headers

```bash
# Apply a 2-second delay and a 10% random failure rate to a single request
curl -X POST http://localhost:8080 \
  -H "X-Echo-Delay: 2000" \
  -H "X-Echo-Chaos: 10" \
  -d '{"data": "test"}'
  ```

# Web Frontend

Navigate to these URLs to use the embedded web frontends.

* WebSocket Client: http://localhost:8080/web-ws

* SSE Client: http://localhost:8080/web-sse