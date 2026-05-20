#!/usr/bin/env sh
set -eu

usage() {
  cat <<'EOF'
Usage:
  sh deploy/docker-build-limited.sh [up|build]

Creates a resource-capped BuildKit builder, then builds the local Docker image.
The script builds the React assets and Go binary first, then packages that binary with deploy/Dockerfile.release.

Tunable environment variables:
  CHATGPT2API_BUILDER_NAME     Builder name (default: chatgpt2api-local-build)
  BUILD_CPUS                   Whole CPU cores available to BuildKit (default: auto, up to 2)
  BUILD_MEMORY                 BuildKit memory limit (default: auto, 2g-4g)
  BUILD_MEMORY_SWAP            BuildKit memory+swap limit (default: auto, 4g when possible)
  BUILDKIT_MAX_PARALLELISM     BuildKit solver parallelism (default: BUILD_CPUS)
  BUILD_GOMAXPROCS             Go compiler parallelism (default: auto, 1 on low-memory hosts, otherwise BUILD_CPUS)
  BUILD_GOMEMLIMIT             Go soft memory limit (default: auto)
  BUILD_GOOS                   Go target OS for the container binary (default: Docker engine OS, fallback linux)
  BUILD_GOARCH                 Go target arch for the container binary (default: Docker engine arch, fallback host arch)
  BUILD_NODE_OPTIONS           Node options for the web build
  BUILD_CPUSET_CPUS            Optional cpuset, for example 0-1

Examples:
  sh deploy/docker-build-limited.sh up
  BUILD_CPUS=2 BUILD_MEMORY=4g BUILD_MEMORY_SWAP=4g BUILD_GOMAXPROCS=2 BUILD_GOMEMLIMIT=2GiB sh deploy/docker-build-limited.sh up
EOF
}

require_uint() {
  name="$1"
  value="$2"
  case "$value" in
    ''|*[!0-9]*)
      echo "$name must be a positive integer, got: $value" >&2
      exit 2
      ;;
    0)
      echo "$name must be greater than zero" >&2
      exit 2
      ;;
  esac
}

command="${1:-up}"
case "$command" in
  up|build)
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

detect_cpu_count() {
  if command -v nproc >/dev/null 2>&1; then
    nproc
  elif command -v getconf >/dev/null 2>&1; then
    getconf _NPROCESSORS_ONLN
  else
    echo 2
  fi
}

detect_memory_mib() {
  if [ -r /proc/meminfo ]; then
    awk '/MemTotal:/ { print int($2 / 1024); exit }' /proc/meminfo
  else
    echo 4096
  fi
}

detect_host_goarch() {
  arch="$(uname -m 2>/dev/null || echo arm64)"
  case "$arch" in
    x86_64|amd64)
      echo amd64
      ;;
    aarch64|arm64)
      echo arm64
      ;;
    armv7l|armv7)
      echo arm
      ;;
    *)
      echo "$arch"
      ;;
  esac
}

detect_docker_target() {
  docker info --format '{{.OSType}} {{.Architecture}}' 2>/dev/null || true
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

detected_cpus="$(detect_cpu_count)"
require_uint detected_cpus "$detected_cpus"
if [ "$detected_cpus" -gt 2 ]; then
  default_build_cpus=2
else
  default_build_cpus="$detected_cpus"
fi

detected_memory_mib="$(detect_memory_mib)"
require_uint detected_memory_mib "$detected_memory_mib"
if [ "$detected_memory_mib" -ge 6144 ]; then
  default_build_memory=4g
  default_build_memory_swap=4g
  default_buildkit_max_parallelism="$default_build_cpus"
  default_build_gomaxprocs="$default_build_cpus"
  default_build_gomemlimit=2GiB
  default_build_node_options=--max-old-space-size=1024
elif [ "$detected_memory_mib" -ge 4096 ]; then
  default_build_memory=3g
  default_build_memory_swap=4g
  default_buildkit_max_parallelism="$default_build_cpus"
  default_build_gomaxprocs="$default_build_cpus"
  default_build_gomemlimit=1536MiB
  default_build_node_options=--max-old-space-size=1024
else
  default_build_memory=2g
  default_build_memory_swap=4g
  default_buildkit_max_parallelism=1
  default_build_gomaxprocs=1
  default_build_gomemlimit=1GiB
  default_build_node_options=--max-old-space-size=768
fi

builder_name="${CHATGPT2API_BUILDER_NAME:-chatgpt2api-local-build}"
build_cpus="${BUILD_CPUS:-$default_build_cpus}"
build_cpu_period="${BUILD_CPU_PERIOD:-100000}"
build_memory="${BUILD_MEMORY:-$default_build_memory}"
build_memory_swap="${BUILD_MEMORY_SWAP:-$default_build_memory_swap}"
buildkit_max_parallelism="${BUILDKIT_MAX_PARALLELISM:-$default_buildkit_max_parallelism}"

require_uint BUILD_CPUS "$build_cpus"
require_uint BUILD_CPU_PERIOD "$build_cpu_period"
require_uint BUILDKIT_MAX_PARALLELISM "$buildkit_max_parallelism"

build_cpu_quota="${BUILD_CPU_QUOTA:-$((build_cpus * build_cpu_period))}"
require_uint BUILD_CPU_QUOTA "$build_cpu_quota"

docker_target="$(detect_docker_target)"
docker_target_os="$(printf '%s\n' "$docker_target" | awk '{ print $1 }')"
docker_target_arch="$(printf '%s\n' "$docker_target" | awk '{ print $2 }')"
case "$docker_target_arch" in
  x86_64)
    docker_target_arch=amd64
    ;;
  aarch64)
    docker_target_arch=arm64
    ;;
esac

export DOCKER_BUILDKIT=1
export BUILDX_BUILDER="$builder_name"
export BUILD_GOMAXPROCS="${BUILD_GOMAXPROCS:-$default_build_gomaxprocs}"
export BUILD_GOMEMLIMIT="${BUILD_GOMEMLIMIT:-$default_build_gomemlimit}"
export BUILD_NODE_OPTIONS="${BUILD_NODE_OPTIONS:-$default_build_node_options}"
export BUILD_GOOS="${BUILD_GOOS:-${docker_target_os:-linux}}"
export BUILD_GOARCH="${BUILD_GOARCH:-${docker_target_arch:-$(detect_host_goarch)}}"
export CHATGPT2API_LOCAL_IMAGE="${CHATGPT2API_LOCAL_IMAGE:-chatgpt2api:local}"
export CHATGPT2API_VERSION="${CHATGPT2API_VERSION:-0.0.0-dev}"

require_uint BUILD_GOMAXPROCS "$BUILD_GOMAXPROCS"

cache_root="${XDG_CACHE_HOME:-${HOME:-.}/.cache}/chatgpt2api-buildkit"
mkdir -p "$cache_root"
buildkit_config="$cache_root/buildkitd.toml"
fingerprint_file="$cache_root/$builder_name.options"

cat > "$buildkit_config" <<EOF
[worker.oci]
  max-parallelism = $buildkit_max_parallelism
EOF

fingerprint="cpu-period=$build_cpu_period
cpu-quota=$build_cpu_quota
memory=$build_memory
memory-swap=$build_memory_swap
max-parallelism=$buildkit_max_parallelism
cpuset-cpus=${BUILD_CPUSET_CPUS:-}"

cat <<EOF
chatgpt2api Docker source build limits:
  builder: $builder_name
  cpu: $build_cpus core(s)
  memory: $build_memory
  memory+swap: $build_memory_swap
  buildkit parallelism: $buildkit_max_parallelism
  go parallelism: $BUILD_GOMAXPROCS
  go memory target: $BUILD_GOMEMLIMIT
  go target: $BUILD_GOOS/$BUILD_GOARCH
  node options: $BUILD_NODE_OPTIONS
  image: $CHATGPT2API_LOCAL_IMAGE
EOF

create_builder() {
  if [ -n "${BUILD_CPUSET_CPUS:-}" ]; then
    docker buildx create \
      --name "$builder_name" \
      --driver docker-container \
      --driver-opt "image=moby/buildkit:buildx-stable-1" \
      --driver-opt "cpu-period=$build_cpu_period" \
      --driver-opt "cpu-quota=$build_cpu_quota" \
      --driver-opt "memory=$build_memory" \
      --driver-opt "memory-swap=$build_memory_swap" \
      --driver-opt "cpuset-cpus=$BUILD_CPUSET_CPUS" \
      --buildkitd-config "$buildkit_config" \
      --use \
      --bootstrap >/dev/null
  else
    docker buildx create \
      --name "$builder_name" \
      --driver docker-container \
      --driver-opt "image=moby/buildkit:buildx-stable-1" \
      --driver-opt "cpu-period=$build_cpu_period" \
      --driver-opt "cpu-quota=$build_cpu_quota" \
      --driver-opt "memory=$build_memory" \
      --driver-opt "memory-swap=$build_memory_swap" \
      --buildkitd-config "$buildkit_config" \
      --use \
      --bootstrap >/dev/null
  fi
  printf '%s' "$fingerprint" > "$fingerprint_file"
}

if docker buildx inspect "$builder_name" >/dev/null 2>&1; then
  if [ ! -f "$fingerprint_file" ] || [ "$(cat "$fingerprint_file")" != "$fingerprint" ]; then
    docker buildx rm --keep-state "$builder_name" >/dev/null 2>&1 || docker buildx rm "$builder_name" >/dev/null
    create_builder
  else
    docker buildx use "$builder_name" >/dev/null
    docker buildx inspect --bootstrap "$builder_name" >/dev/null
  fi
else
  create_builder
fi

mkdir -p "$repo_root/.deploy"

(
  cd "$repo_root/web"
  if [ -f package-lock.json ]; then
    npm ci
  else
    npm install --no-audit --no-fund
  fi
  NODE_OPTIONS="$BUILD_NODE_OPTIONS" npm run build
)

(
  cd "$repo_root"
  GOOS="$BUILD_GOOS" GOARCH="$BUILD_GOARCH" CGO_ENABLED="${CGO_ENABLED:-0}" \
  GOMAXPROCS="$BUILD_GOMAXPROCS" GOMEMLIMIT="$BUILD_GOMEMLIMIT" \
    go build -p="$BUILD_GOMAXPROCS" -trimpath -tags=embed \
    -ldflags="-s -w -X chatgpt2api/internal/version.Version=$CHATGPT2API_VERSION" \
    -o "$repo_root/.deploy/chatgpt2api" ./internal
)

docker buildx build \
  --builder "$builder_name" \
  --load \
  --tag "$CHATGPT2API_LOCAL_IMAGE" \
  --file "$repo_root/deploy/Dockerfile.release" \
  "$repo_root/.deploy"

if [ "$command" = "up" ]; then
  CHATGPT2API_DATA_DIR="$repo_root/data" \
  CHATGPT2API_ENV_FILE="$repo_root/.env" \
  CHATGPT2API_IMAGE="$CHATGPT2API_LOCAL_IMAGE" \
  CHATGPT2API_PULL_POLICY=never \
  docker compose --env-file "$repo_root/.env" -f "$repo_root/deploy/docker-compose.yml" up -d --no-build
fi
