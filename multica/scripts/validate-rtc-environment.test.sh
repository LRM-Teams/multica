#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
validator="${script_dir}/validate-rtc-environment.sh"
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
optional_names=(
  VOLCENGINE_RTC_SESSION_TOKEN
  VOLCENGINE_RTC_ENDPOINT
  VOLCENGINE_RTC_REGION
  VOLCENGINE_RTC_ARK_ENDPOINT_ID
  VOLCENGINE_RTC_ARK_MODEL_NAME
  VOLCENGINE_RTC_TTS_VOICE_ID
  VOLCENGINE_RTC_CALLBACK_URL
)

clean_environment=(env)
for name in "${required_names[@]}" "${optional_names[@]}"; do
  clean_environment+=(-u "$name")
done

disabled_output="$("${clean_environment[@]}" bash "$validator")"
if [[ "$disabled_output" != *"RTC is disabled"* ]]; then
  echo "Disabled RTC validation did not report the disabled state."
  exit 1
fi

complete_environment=("${clean_environment[@]}")
for name in "${required_names[@]}"; do
  complete_environment+=("$name=test-value")
done
complete_environment+=(VOLCENGINE_RTC_ARK_ENDPOINT_ID=ep-test)
complete_output="$("${complete_environment[@]}" bash "$validator")"
if [[ "$complete_output" != *"configuration is complete"* ]]; then
  echo "Complete RTC validation did not report success."
  exit 1
fi

set +e
partial_output="$(
  "${clean_environment[@]}" \
    VOLCENGINE_RTC_APP_ID=must-not-be-printed \
    VOLCENGINE_RTC_APP_KEY='   ' \
    bash "$validator" 2>&1
)"
partial_status=$?
set -e
if [[ "$partial_status" -eq 0 ]]; then
  echo "Partial RTC validation unexpectedly succeeded."
  exit 1
fi
if [[ "$partial_output" != *"VOLCENGINE_RTC_APP_KEY"* ]] ||
  [[ "$partial_output" != *"VOLCENGINE_RTC_ACCESS_KEY_ID"* ]]; then
  echo "Partial RTC validation did not identify missing secret names."
  exit 1
fi
if [[ "$partial_output" == *"must-not-be-printed"* ]]; then
  echo "Partial RTC validation leaked a configured secret value."
  exit 1
fi

set +e
api_key_only_output="$(
  "${clean_environment[@]}" \
    VOLCENGINE_RTC_APP_ID=test-value \
    VOLCENGINE_RTC_APP_KEY=test-value \
    VOLCENGINE_RTC_ACCESS_KEY_ID=test-value \
    VOLCENGINE_RTC_SECRET_ACCESS_KEY=test-value \
    VOLCENGINE_RTC_ASR_APP_ID=test-value \
    VOLCENGINE_RTC_TTS_APP_ID=test-value \
    VOLCENGINE_RTC_CALLBACK_SIGNATURE=test-value \
    VOLCENGINE_RTC_ARK_ENDPOINT_ID=ep-test \
    DOUBAO_SPEECH_API_KEY=new-console-api-key-must-not-be-reused \
    bash "$validator" 2>&1
)"
api_key_only_status=$?
set -e
if [[ "$api_key_only_status" -eq 0 ]] ||
  [[ "$api_key_only_output" != *"VOLCENGINE_RTC_SPEECH_ACCESS_TOKEN"* ]] ||
  [[ "$api_key_only_output" == *"new-console-api-key-must-not-be-reused"* ]]; then
  echo "Doubao API key was accepted in place of the RTC speech access token."
  exit 1
fi

set +e
optional_only_output="$(
  "${clean_environment[@]}" \
    VOLCENGINE_RTC_ARK_ENDPOINT_ID=must-not-be-printed \
    bash "$validator" 2>&1
)"
optional_only_status=$?
set -e
if [[ "$optional_only_status" -eq 0 ]]; then
  echo "Optional-only RTC validation unexpectedly succeeded."
  exit 1
fi
if [[ "$optional_only_output" != *"VOLCENGINE_RTC_APP_ID"* ]] ||
  [[ "$optional_only_output" == *"must-not-be-printed"* ]]; then
  echo "Optional-only RTC validation did not fail safely."
  exit 1
fi

set +e
missing_ark_output="$(
  "${clean_environment[@]}" \
    "${required_names[0]}=test-value" \
    "${required_names[1]}=test-value" \
    "${required_names[2]}=test-value" \
    "${required_names[3]}=test-value" \
    "${required_names[4]}=test-value" \
    "${required_names[5]}=test-value" \
    "${required_names[6]}=test-value" \
    "${required_names[7]}=test-value" \
    bash "$validator" 2>&1
)"
missing_ark_status=$?
set -e
if [[ "$missing_ark_status" -eq 0 ]] ||
  [[ "$missing_ark_output" != *"VOLCENGINE_RTC_ARK_ENDPOINT_ID"* ]]; then
  echo "Missing Ark endpoint did not fail safely."
  exit 1
fi

set +e
display_model_output="$(
  "${clean_environment[@]}" \
    "${required_names[0]}=test-value" \
    "${required_names[1]}=test-value" \
    "${required_names[2]}=test-value" \
    "${required_names[3]}=test-value" \
    "${required_names[4]}=test-value" \
    "${required_names[5]}=test-value" \
    "${required_names[6]}=test-value" \
    "${required_names[7]}=test-value" \
    VOLCENGINE_RTC_ARK_MODEL_NAME=Doubao-Seed \
    bash "$validator" 2>&1
)"
display_model_status=$?
set -e
if [[ "$display_model_status" -eq 0 ]] ||
  [[ "$display_model_output" != *"not a callable endpoint"* ]]; then
  echo "Ark display model did not fail safely."
  exit 1
fi

echo "rtc environment validation ok"
