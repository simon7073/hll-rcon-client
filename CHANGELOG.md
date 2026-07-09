# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v0.1.0] - 2026-07-09

### Layer 0 — Core (`core/`)
- TCP transport with auto-reconnect
- HLL RCONv2 packet framing & XOR encryption
- SSRF protection (RFC 1918 / loopback / link-local blocked)

### Layer 1 — RCON Client (`rcon/`)
- Connection pool with circuit breaker
- Error classification (retryable / fatal / auth)
- Exponential backoff with jitter

### Layer 2 — CLI (`cmd/rcon-cli/`)
- `discover` — auto-discover RCON command schema
- `exec` — execute single command
- `batch` — batch execute from file
- `ping` — connection diagnostics
- `repl` — interactive REPL mode

### Layer 3 — HTTP Proxy (`cmd/rcon-proxy/`)
- RESTful API for cross-language access
- Token-based authentication
- Request rate limiting

### Tooling
- `verify_layers` — integration test suite for all layers
- GitHub Actions CI: multi-platform build (Linux / Windows / macOS × amd64 / arm64)
