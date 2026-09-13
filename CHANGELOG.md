# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [Unreleased]

### Fixed

- **`syslog_received_total` always 0**: the receive counter was never
  incremented because the batch queue was created with a `nil` recorder (the
  metrics instance was built after the queue). The metrics instance is now
  created first and bound to the queue as its `Recorder`, so
  `syslog_received_total` / `syslog_forward_total` / `syslog_dropped_total`
  accumulate correctly on the receive path.

### Added

- **CHANGELOG.md**: this file, to track notable changes going forward.

---

## [0.1.0] - 2026-09-13

### Added

- Initial release: high-throughput UDP syslog collector that parses
  RFC3164 / RFC5424 / vendor-lenient messages and forwards them to cep-engine.
- Prometheus metrics (`/metrics`): received / forwarded / failed / dropped
  totals, last-5min throughput, queue depth.
- Bounded batch queue with backpressure and exponential-backoff HTTP forwarding.
- Active-Active support via deterministic `originTimestamp`.

### Fixed

- RFC3164 year parsing; removed content hash from `originTimestamp` (kept the
  syslog header timestamp for determinism).
