#!/usr/bin/env sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "사용법: $0 kkiit-v버전.tar.gz" >&2
  exit 2
fi
gzip -dc -- "$1" | docker load
