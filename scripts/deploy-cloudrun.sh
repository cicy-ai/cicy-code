#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SERVICE="${SERVICE:-cicy-code-runtime}"
REGION="${REGION:-asia-east1}"
PROJECT="${PROJECT:-$(gcloud config get-value project 2>/dev/null || true)}"
IMAGE="${IMAGE:-gcr.io/${PROJECT}/${SERVICE}:$(date +%Y%m%d-%H%M%S)}"
PLATFORM="${PLATFORM:-managed}"
PUBLIC_URL="${CICY_PUBLIC_URL:-}"

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

required_env PROJECT
required_env CICY_MASTER_URL
required_env CICY_MASTER_TOKEN
required_env CICY_API_TOKEN

echo "==> Build image: $IMAGE"
gcloud builds submit "$ROOT_DIR" --tag "$IMAGE" --file "$ROOT_DIR/api/Dockerfile.cloudrun"

env_vars="CICY_RUNTIME_KIND=cloudrun,CICY_MASTER_URL=${CICY_MASTER_URL},CICY_MASTER_TOKEN=${CICY_MASTER_TOKEN},CICY_API_TOKEN=${CICY_API_TOKEN}${CICY_INSTANCE_KEY:+,CICY_INSTANCE_KEY=${CICY_INSTANCE_KEY}}${CICY_INSTANCE_LABEL:+,CICY_INSTANCE_LABEL=${CICY_INSTANCE_LABEL}}"
if [ -n "$PUBLIC_URL" ]; then
  env_vars=","$env_vars
  env_vars="CICY_PUBLIC_URL=${PUBLIC_URL}${env_vars}"
fi

echo "==> Deploy Cloud Run: $SERVICE ($REGION)"
gcloud run deploy "$SERVICE" \
  --project "$PROJECT" \
  --region "$REGION" \
  --platform "$PLATFORM" \
  --image "$IMAGE" \
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

if [ -n "${FREE_API_HOST:-}" ]; then
  FREE_API_ENV_KEY="$(free_api_env_key "$FREE_API_HOST")"
  echo "==> Worker env suggestion"
  echo "$FREE_API_ENV_KEY=$FINAL_PUBLIC_URL"
fi
