#!/bin/sh
set -eu

# Some runner/proxy paths reset HTTP/2 streams. Limit this workaround to Go's
# download process; module and checksum verification stay enabled.
attempt=1
delay=5
while :; do
    if GODEBUG="${GODEBUG:+$GODEBUG,}http2client=0" go mod download; then
        exit 0
    fi
    if [ "$attempt" -ge 4 ]; then
        echo 'Go module download failed after four attempts.' >&2
        exit 1
    fi
    echo "Go module download failed; retrying in ${delay}s (${attempt}/4)." >&2
    sleep "$delay"
    attempt=$((attempt + 1))
    delay=$((delay * 2))
done
