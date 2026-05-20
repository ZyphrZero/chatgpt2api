#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  sh deploy/smoke-routes.sh [base_url]

Checks the health endpoint, key frontend routes, and same-origin static assets
referenced by those routes for a running deployment.

Environment:
  CHATGPT2API_PORT      Local port used when base_url is omitted.
  SMOKE_ROUTES          Space-separated route list to check.
  SMOKE_CHECK_ASSETS    Set to 0 to skip same-origin asset checks.
  SMOKE_MIN_ROUTE_BYTES Minimum bytes expected for frontend route HTML.

EOF
}

case "${1:-}" in
  -h|--help|help)
    usage
    exit 0
    ;;
esac

base_url="${1:-http://127.0.0.1:${CHATGPT2API_PORT:-3000}}"
routes="${SMOKE_ROUTES:-/ /login /image /gallery /subscription /settings}"
min_route_bytes="${SMOKE_MIN_ROUTE_BYTES:-1000}"

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 2
fi

base_url="${base_url%/}"
tmp_body="$(mktemp -t 1818-smoke-body.XXXXXX)"
tmp_error="$(mktemp -t 1818-smoke-error.XXXXXX)"
tmp_headers="$(mktemp -t 1818-smoke-headers.XXXXXX)"
tmp_assets="$(mktemp -t 1818-smoke-assets.XXXXXX)"
tmp_asset_seen="$(mktemp -t 1818-smoke-assets-seen.XXXXXX)"
trap 'rm -f "$tmp_body" "$tmp_error" "$tmp_headers" "$tmp_assets" "$tmp_asset_seen"' EXIT INT TERM

failures=0

check_url() {
  label="$1"
  url="$2"
  : > "$tmp_error"
  : > "$tmp_headers"
  code="$(curl -fsS -D "$tmp_headers" -o "$tmp_body" -w '%{http_code}' "$url" 2>"$tmp_error" || true)"
  bytes="$(wc -c < "$tmp_body" | tr -d ' ')"
  if [ "$code" = "200" ]; then
    printf 'ok %s %s %s bytes\n' "$label" "$code" "$bytes"
  else
    failures=$((failures + 1))
    printf 'fail %s %s %s bytes %s\n' "$label" "${code:-000}" "$bytes" "$(cat "$tmp_error")"
  fi
}

content_type() {
  awk 'tolower($0) ~ /^content-type:/ {sub(/\r$/, ""); print; exit}' "$tmp_headers"
}

check_min_bytes() {
  label="$1"
  min_bytes="$2"
  bytes="$(wc -c < "$tmp_body" | tr -d ' ')"
  if [ "$bytes" -lt "$min_bytes" ]; then
    failures=$((failures + 1))
    printf 'fail %s body too small: %s bytes < %s\n' "$label" "$bytes" "$min_bytes"
  fi
}

check_content_type() {
  label="$1"
  expected="$2"
  actual="$(content_type)"
  if ! printf '%s\n' "$actual" | grep -qi "$expected"; then
    failures=$((failures + 1))
    printf 'fail %s content-type %s did not match %s\n' "$label" "${actual:-missing}" "$expected"
  fi
}

check_body_contains() {
  label="$1"
  pattern="$2"
  if ! grep -q "$pattern" "$tmp_body"; then
    failures=$((failures + 1))
    printf 'fail %s body missing %s\n' "$label" "$pattern"
  fi
}

collect_same_origin_assets() {
  grep -Eo '(src|href)="[^"]+"' "$tmp_body" \
    | sed -E 's/^[^"]+"([^"]+)".*/\1/' \
    | grep -E '^/[^/]' \
    | grep -Ev '^/(api|health)(/|$)' \
    >> "$tmp_assets" || true
}

check_url "/health" "$base_url/health"
if ! grep -q '"status":"ok"' "$tmp_body"; then
  failures=$((failures + 1))
  printf 'fail /health body did not contain status ok\n'
fi

for route in $routes; do
  check_url "$route" "$base_url$route"
  check_min_bytes "$route" "$min_route_bytes"
  check_body_contains "$route" '/assets/'
  collect_same_origin_assets
done

if [ "${SMOKE_CHECK_ASSETS:-1}" != "0" ]; then
  sort -u "$tmp_assets" > "$tmp_asset_seen"
  while IFS= read -r asset_path; do
    [ -n "$asset_path" ] || continue
    check_url "asset:$asset_path" "$base_url$asset_path"
    check_min_bytes "asset:$asset_path" 1
    case "$asset_path" in
      *.js) check_content_type "asset:$asset_path" 'javascript' ;;
      *.css) check_content_type "asset:$asset_path" 'text/css' ;;
      *.ico) check_content_type "asset:$asset_path" 'image/' ;;
    esac
  done < "$tmp_asset_seen"
fi

if [ "$failures" -gt 0 ]; then
  printf 'smoke failed: %s failure(s)\n' "$failures" >&2
  exit 1
fi

printf 'smoke passed: %s\n' "$base_url"
