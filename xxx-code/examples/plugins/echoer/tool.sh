#!/bin/sh
set -eu

payload="$(cat)"
if [ -z "$payload" ]; then
  payload='{}'
fi

printf '{"content": %s}\n' "$payload"
