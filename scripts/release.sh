#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=$(tr -d '[:space:]' < "$project_root/VERSION")
case "$version" in
  ''|*[!0-9A-Za-z.-]*) echo "VERSION 값이 올바르지 않습니다." >&2; exit 1 ;;
esac

image="kkiit:v${version}"
archive="$project_root/release/kkiit-v${version}.tar.gz"
mkdir -p "$project_root/release"
docker build \
  --build-arg "VERSION=$version" \
  --build-arg "COMMIT=$(git -C "$project_root" rev-parse --short HEAD 2>/dev/null || echo unknown)" \
  --build-arg "BUILT_AT=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --tag "$image" "$project_root"
docker save "$image" | gzip -9 > "$archive"
echo "$archive"
