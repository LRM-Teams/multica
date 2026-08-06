#!/usr/bin/env bash
set -euo pipefail

required_names=(
  VOLCENGINE_RTC_APP_ID
  VOLCENGINE_RTC_APP_KEY
  VOLCENGINE_RTC_ACCESS_KEY_ID
  VOLCENGINE_RTC_SECRET_ACCESS_KEY
  VOLCENGINE_RTC_ASR_APP_ID
  VOLCENGINE_RTC_TTS_APP_ID
  VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN
  VOLCENGINE_RTC_CALLBACK_SIGNATURE
)
opt_in_names=(
  "${required_names[@]}"
  VOLCENGINE_RTC_SESSION_TOKEN
  VOLCENGINE_RTC_ENDPOINT
  VOLCENGINE_RTC_REGION
  VOLCENGINE_RTC_ARK_ENDPOINT_ID
  VOLCENGINE_RTC_ARK_MODEL_NAME
  VOLCENGINE_RTC_TTS_VOICE_ID
  VOLCENGINE_RTC_CALLBACK_URL
)

configured_count=0
for name in "${opt_in_names[@]}"; do
  value="${!name-}"
  value_without_whitespace="${value//[[:space:]]/}"
  if [[ -n "$value_without_whitespace" ]]; then
    configured_count=$((configured_count + 1))
  fi
done

if [[ "$configured_count" -eq 0 ]]; then
  echo "Volcengine RTC is disabled; no RTC secrets are configured."
  exit 0
fi

missing_names=()
for name in "${required_names[@]}"; do
  value="${!name-}"
  value_without_whitespace="${value//[[:space:]]/}"
  if [[ -z "$value_without_whitespace" ]]; then
    missing_names+=("$name")
  fi
done

if [[ "${#missing_names[@]}" -gt 0 ]]; then
  printf 'Volcengine RTC secrets must be configured together; missing:' >&2
  printf ' %s' "${missing_names[@]}" >&2
  printf '\n' >&2
  exit 1
fi

ark_endpoint_id="${VOLCENGINE_RTC_ARK_ENDPOINT_ID-}"
ark_endpoint_id="${ark_endpoint_id//[[:space:]]/}"
ark_model_name="${VOLCENGINE_RTC_ARK_MODEL_NAME-}"
ark_model_name="${ark_model_name//[[:space:]]/}"
if [[ -n "$ark_model_name" ]]; then
  echo "VOLCENGINE_RTC_ARK_MODEL_NAME is a display model name, not a callable endpoint; configure VOLCENGINE_RTC_ARK_ENDPOINT_ID." >&2
  exit 1
fi
if [[ -z "$ark_endpoint_id" ]]; then
  echo "Volcengine RTC requires VOLCENGINE_RTC_ARK_ENDPOINT_ID." >&2
  exit 1
fi

echo "Volcengine RTC secret configuration is complete."
