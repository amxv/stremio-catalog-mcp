#!/usr/bin/env bash
# Optional Firebase Spark + FCM provisioning, separate from core MCP setup.
# This intentionally never deploys production or touches any TV.
set -euo pipefail

usage() {
  cat <<'HELP'
Usage: scripts/setup-tv-fcm.sh --project YOUR_UNIQUE_PROJECT_ID [--apply] [--app-id FIREBASE_ANDROID_APP_ID]

Preview by default. --apply creates/reuses Firebase project, Android app,
Firebase public configuration, and an FCM-only service account/private key.
Prerequisites: firebase, gcloud, jq; authenticate via firebase/gcloud login.
This does NOT enable TV_OPEN_ENABLED, deploy Vercel, or upgrade billing.
HELP
}

project=""
app_id=""
apply=false
while (($#)); do
  case "$1" in
    --project) project="$2"; shift 2 ;;
    --app-id) app_id="$2"; shift 2 ;;
    --apply) apply=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
  esac
done
if [[ ! "$project" =~ ^[a-z][a-z0-9-]{4,29}$ ]]; then
  printf 'Supply a valid, globally unique Firebase project ID.\n' >&2
  exit 2
fi
root_dir="$(cd "$(dirname "$0")/.." && pwd)"
firebase_config="$root_dir/android-tv/app/google-services.json"
service_account="stremio-tv-sender@$project.iam.gserviceaccount.com"
private_dir="$HOME/.config/stremio-tv-fcm"
private_file="$private_dir/$project-sender.json"

printf 'FCM project: %s\nPackage: xyz.ashray.stremiotvremote\n' "$project"
printf 'Android config: %s\nPrivate server key: %s\n' "$firebase_config" "$private_file"
if [[ "$apply" != true ]]; then
  printf '\nPREVIEW ONLY. Nothing changed. Re-run with --apply.\n'
  exit 0
fi
for program in firebase gcloud jq; do
  if ! command -v "$program" >/dev/null 2>&1; then
    printf 'Missing required CLI: %s\n' "$program" >&2
    exit 1
  fi
done

if ! firebase projects:list --json | jq -e --arg id "$project" '.result[]? | select(.projectId == $id)' >/dev/null; then
  firebase projects:create "$project" --display-name "Stremio TV Remote"
fi

if [[ -z "$app_id" ]]; then
  # Explicit --app-id is required when a project has multiple Android apps.
  android_apps="$(firebase apps:list ANDROID --project "$project" --json |
      jq -r '.result[]? | select(.platform == "ANDROID") | .appId')"
  if [[ -z "$android_apps" ]]; then
    firebase apps:create ANDROID "Stremio TV Remote" \
      --package-name xyz.ashray.stremiotvremote --project "$project"
    android_apps="$(firebase apps:list ANDROID --project "$project" --json |
        jq -r '.result[]? | select(.platform == "ANDROID") | .appId')"
  fi
  if [[ -z "$android_apps" || "$android_apps" == *$'\n'* ]]; then
    printf 'Project has multiple or no Android apps; pass --app-id to select explicitly.\n' >&2
    exit 1
  fi
  app_id="$android_apps"
fi

if [[ -f "$firebase_config" ]]; then
  existing_project="$(jq -r '.project_info.project_id // empty' "$firebase_config")"
  if [[ "$existing_project" != "$project" ]]; then
    printf 'google-services.json belongs to another project; move it aside first.\n' >&2
    exit 1
  fi
  printf 'Reusing existing public Firebase app config.\n'
else
  firebase apps:sdkconfig ANDROID "$app_id" --project "$project" --out "$firebase_config"
fi

gcloud services enable fcm.googleapis.com --project "$project" >/dev/null
if ! gcloud iam service-accounts describe "$service_account" --project "$project" >/dev/null 2>&1; then
  gcloud iam service-accounts create stremio-tv-sender \
    --project "$project" --display-name "Stremio TV FCM sender"
fi
gcloud projects add-iam-policy-binding "$project" \
  --member "serviceAccount:$service_account" \
  --role roles/firebasecloudmessaging.admin --quiet >/dev/null

umask 077
mkdir -p "$private_dir"
if [[ -e "$private_file" ]]; then
  printf 'Reusing the existing private FCM key file.\n'
else
  gcloud iam service-accounts keys create "$private_file" \
    --project "$project" --iam-account "$service_account" >/dev/null
  chmod 600 "$private_file"
fi
printf '\nProvisioned. No paid Firebase tier needed.\n'
printf 'Set FCM_PROJECT_ID=%s on your Go Vercel project.\n' "$project"
printf 'Store %s as FCM_SERVICE_ACCOUNT_JSON (Secret).\n' "$private_file"
printf 'Set TV_OPEN_ENABLED=true only when you intentionally deploy.\n'
