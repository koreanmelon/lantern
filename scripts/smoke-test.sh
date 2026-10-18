#!/usr/bin/env bash
set -euo pipefail

# The Go test builds a native binary, uses temporary record state and ephemeral
# loopback ports, restarts the app, and cleans up its own child processes.
go -C "$(dirname "${BASH_SOURCE[0]}")/.." test -tags=integration \
  ./tests/integration -run '^TestCompiledAppPersistence$' -count=1 -v
