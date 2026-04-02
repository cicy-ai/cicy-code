#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
GLOBAL_JSON="${HOME}/global.json"
read_global_json_key() {
  local key="$1"
  if [ -f "$GLOBAL_JSON" ]; then
    python3 - "$GLOBAL_JSON" "$key" <<'PY'
import json, sys
path, key = sys.argv[1], sys.argv[2]
try:
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    value = data.get(key, "")
    if value is None:
        value = ""
    print(value, end="")
except Exception:
    pass
PY
  fi
}
global_json_value() {
  local expr="$1"
  if [ -f "$GLOBAL_JSON" ]; then
    python3 - "$GLOBAL_JSON" "$expr" <<'PY'
import json, sys
path, expr = sys.argv[1], sys.argv[2]
try:
    with open(path, "r", encoding="utf-8") as f:
        data = json.load(f)
    value = data
    for part in expr.split('.'):
        if not part:
            continue
        if isinstance(value, dict):
            value = value.get(part, "")
        else:
            value = ""
            break
    if value is None:
        value = ""
    print(value, end="")
except Exception:
    pass
PY
  fi
}
SERVICE="${SERVICE:-$(global_json_value cicy-cluster.service)}"
SERVICE="${SERVICE:-cicy-code-runtime}"
BASE_SERVICE="${BASE_SERVICE:-cicy-code-base}"
REGION="${REGION:-$(global_json_value cicy-cluster.region)}"
REGION="${REGION:-asia-east1}"
PROJECT="${PROJECT:-$(global_json_value cicy-cluster.project_id)}"
PROJECT="${PROJECT:-$(read_global_json_key project_id)}"
IMAGE="${IMAGE:-$(global_json_value cicy-cluster.image)}"
IMAGE="${IMAGE:-$(global_json_value cicy-cluster.image_repository)}"
IMAGE="${IMAGE:-gcr.io/${PROJECT}/${SERVICE}:$(date +%Y%m%d-%H%M%S)}"
BASE_IMAGE="${BASE_IMAGE:-gcr.io/${PROJECT}/${BASE_SERVICE}:latest}"
PLATFORM="${PLATFORM:-managed}"
MEMORY="${MEMORY:-$(global_json_value cicy-cluster.memory)}"
MEMORY="${MEMORY:-2Gi}"
MAX_INSTANCES="${MAX_INSTANCES:-$(global_json_value cicy-cluster.max_instances)}"
MAX_INSTANCES="${MAX_INSTANCES:-1}"
MIN_INSTANCES="${MIN_INSTANCES:-$(global_json_value cicy-cluster.min_instances)}"
MIN_INSTANCES="${MIN_INSTANCES:-1}"
CONCURRENCY="${CONCURRENCY:-$(global_json_value cicy-cluster.concurrency)}"
CONCURRENCY="${CONCURRENCY:-1}"
PUBLIC_URL="${CICY_PUBLIC_URL:-$(global_json_value cicy-cluster.service_url)}"
PREBUILD="${PREBUILD:-0}"
BUILD_IMAGE="${BUILD_IMAGE:-0}"
BUILD_BASE="${BUILD_BASE:-0}"

required_env() {
  local name="$1"
  if [ -z "${!name:-}" ]; then
    echo "missing env: $name" >&2
    exit 1
  fi
}

free_api_env_key() {
  local host="$1"
  local sub="${host%%.*}"
  sub="$(printf '%s' "$sub" | tr '[:lower:]-' '[:upper:]_')"
  printf 'FREE_API_BACKEND_%s' "$sub"
}

join_env_vars() {
  local IFS=,
  printf '%s' "$*"
}

required_env PROJECT
required_env IMAGE
CICY_API_TOKEN="${CICY_API_TOKEN:-$(global_json_value cicy-cluster.api_token)}"
CICY_INSTANCE_KEY="${CICY_INSTANCE_KEY:-$(global_json_value cicy-cluster.instance_key)}"
CICY_INSTANCE_LABEL="${CICY_INSTANCE_LABEL:-$(global_json_value cicy-cluster.instance_label)}"
required_env CICY_API_TOKEN

if [ "$PREBUILD" = "1" ]; then
  echo "==> Local build: ./build.sh build linux amd64"
  (cd "$ROOT_DIR" && ./build.sh build linux amd64)
fi

ENV_ITEMS=(
  "CICY_RUNTIME_KIND=cloudrun"
  "CICY_API_TOKEN=${CICY_API_TOKEN}"
)

if [ -n "${CICY_INSTANCE_KEY:-}" ]; then
  ENV_ITEMS+=("CICY_INSTANCE_KEY=${CICY_INSTANCE_KEY}")
fi
if [ -n "${CICY_INSTANCE_LABEL:-}" ]; then
  ENV_ITEMS+=("CICY_INSTANCE_LABEL=${CICY_INSTANCE_LABEL}")
fi
if [ -n "$PUBLIC_URL" ]; then
  ENV_ITEMS+=("CICY_PUBLIC_URL=${PUBLIC_URL}")
fi

# Backward compatibility: allow master wiring only if explicitly provided.
if [ -n "${CICY_MASTER_URL:-}" ]; then
  ENV_ITEMS+=("CICY_MASTER_URL=${CICY_MASTER_URL}")
fi
if [ -n "${CICY_MASTER_TOKEN:-}" ]; then
  ENV_ITEMS+=("CICY_MASTER_TOKEN=${CICY_MASTER_TOKEN}")
fi

env_vars="$(join_env_vars "${ENV_ITEMS[@]}")"

if [ "$BUILD_IMAGE" = "1" ]; then
  echo "==> Build image: $IMAGE"
  TMP_CLOUDBUILD="$(mktemp)"
  TMP_STAGE_DIR="$(mktemp -d)"
  cleanup() {
    rm -f "$TMP_CLOUDBUILD"
    rm -rf "$TMP_STAGE_DIR"
  }
  trap cleanup EXIT

  mkdir -p "$TMP_STAGE_DIR/api"
  cp "$ROOT_DIR/api/Dockerfile.cloudrun" "$TMP_STAGE_DIR/api/"
  cp "$ROOT_DIR/api/Dockerfile.cloudrun.base" "$TMP_STAGE_DIR/api/"
  cp "$ROOT_DIR/api/setup-agent.sh" "$TMP_STAGE_DIR/api/"
  cp "$ROOT_DIR/api/cicy-code" "$TMP_STAGE_DIR/api/cicy-code-docker"

  cat > "$TMP_CLOUDBUILD" <<EOF
steps:
EOF

  if [ "$BUILD_BASE" = "1" ]; then
cat >> "$TMP_CLOUDBUILD" <<EOF
  - name: gcr.io/cloud-builders/docker
    args: ['build', '-f', 'api/Dockerfile.cloudrun.base', '-t', '$BASE_IMAGE', 'api']
EOF
  fi

  cat >> "$TMP_CLOUDBUILD" <<EOF
  - name: gcr.io/cloud-builders/docker
    args: ['build', '-f', 'api/Dockerfile.cloudrun', '--build-arg', 'BASE_IMAGE=$BASE_IMAGE', '-t', '$IMAGE', 'api']
images:
EOF

  if [ "$BUILD_BASE" = "1" ]; then
cat >> "$TMP_CLOUDBUILD" <<EOF
  - '$BASE_IMAGE'
EOF
  fi

  cat >> "$TMP_CLOUDBUILD" <<EOF
  - '$IMAGE'
EOF
  gcloud builds submit "$TMP_STAGE_DIR" --config "$TMP_CLOUDBUILD"
else
  echo "==> Skip image build, deploy existing image: $IMAGE"
fi

echo "==> Deploy Cloud Run: $SERVICE ($REGION)"
gcloud run deploy "$SERVICE" \
  --project "$PROJECT" \
  --region "$REGION" \
  --platform "$PLATFORM" \
  --image "$IMAGE" \
  --memory "$MEMORY" \
  --min-instances "$MIN_INSTANCES" \
  --max-instances "$MAX_INSTANCES" \
  --concurrency "$CONCURRENCY" \
  --allow-unauthenticated \
  --port 8080 \
  --set-env-vars "$env_vars"

SERVICE_URL="$(gcloud run services describe "$SERVICE" --project "$PROJECT" --region "$REGION" --format='value(status.url)')"
echo "==> Service URL: $SERVICE_URL"

FINAL_PUBLIC_URL="$PUBLIC_URL"
if [ -z "$FINAL_PUBLIC_URL" ]; then
  echo "==> Backfill CICY_PUBLIC_URL with service URL"
  gcloud run services update "$SERVICE" \
    --project "$PROJECT" \
    --region "$REGION" \
    --update-env-vars "$env_vars,CICY_PUBLIC_URL=${SERVICE_URL}"
  FINAL_PUBLIC_URL="$SERVICE_URL"
fi

echo "$FINAL_PUBLIC_URL"
echo "==> Register this node locally with:"
echo "python3 skills/cicy-master register ${CICY_INSTANCE_KEY:-$SERVICE} \"$FINAL_PUBLIC_URL\" --token \"$CICY_API_TOKEN\"${CICY_INSTANCE_LABEL:+ --label \"$CICY_INSTANCE_LABEL\"}"

if [ -n "${FREE_API_HOST:-}" ]; then
  FREE_API_ENV_KEY="$(free_api_env_key "$FREE_API_HOST")"
  echo "==> Worker env suggestion"
  echo "$FREE_API_ENV_KEY=$FINAL_PUBLIC_URL"
fi
