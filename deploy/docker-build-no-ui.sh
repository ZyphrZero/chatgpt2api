#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  sh deploy/docker-build-no-ui.sh [build|up]

Builds from a temporary copy of the current worktree while restoring selected
UI files from HEAD. This is for testing non-UI changes without shipping the
current homepage/login UI edits.

Environment:
  NO_UI_RESTORE_FILES  Space-separated files to restore from HEAD.
  NO_UI_TMP_ROOT       Parent directory for temporary copies.
  NO_UI_KEEP_TMP        Set to 1 to keep the temporary build copy after build.
                        up mode always keeps it because compose mounts .env/data.

EOF
}

command="${1:-build}"
case "$command" in
  build|up)
    ;;
  -h|--help|help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
tmp_parent="${NO_UI_TMP_ROOT:-/tmp}"
tmp_dir="$tmp_parent/1818-pro-no-ui-$(date +%Y%m%d%H%M%S)"

restore_files="${NO_UI_RESTORE_FILES:-web/src/app/page.tsx web/src/app/login/page.tsx web/src/components/login-page-image-stage.tsx}"

if ! command -v git >/dev/null 2>&1; then
  echo "git is required" >&2
  exit 2
fi

if ! git -C "$repo_root" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "repo_root is not a git worktree: $repo_root" >&2
  exit 2
fi

mkdir -p "$tmp_parent"
umask 077
rm -rf "$tmp_dir"
mkdir -p "$tmp_dir"

cleanup() {
  if [ "$command" != "up" ] && [ "${NO_UI_KEEP_TMP:-0}" != "1" ]; then
    rm -rf "$tmp_dir"
  fi
}
trap cleanup EXIT INT TERM

rsync -a --delete \
  --exclude '.git' \
  --exclude 'web/node_modules' \
  --exclude 'internal/web/dist' \
  --exclude 'web-rs/target' \
  --exclude '.deploy' \
  "$repo_root/" "$tmp_dir/"
chmod 700 "$tmp_dir"

for file in $restore_files; do
  mkdir -p "$tmp_dir/$(dirname "$file")"
  git -C "$repo_root" show "HEAD:$file" > "$tmp_dir/$file"
done

if [ ! -f "$tmp_dir/.env" ]; then
  smoke_password="smoke_$(date +%s)_$$"
  {
    printf '%s\n' '# Temporary smoke-test env. Secrets intentionally not copied.'
    printf '%s\n' 'STORAGE_BACKEND=sqlite'
    printf '%s\n' 'CHATGPT2API_ADMIN_USERNAME=admin'
    printf '%s\n' "CHATGPT2API_ADMIN_PASSWORD=$smoke_password"
  } > "$tmp_dir/.env"
  chmod 600 "$tmp_dir/.env"
fi

cat <<EOF
non-UI Docker build context:
  source: $repo_root
  temp: $tmp_dir
  restored:
$(printf '    %s\n' $restore_files)
EOF

(
  cd "$tmp_dir"
  sh deploy/docker-build-limited.sh "$command"
)
