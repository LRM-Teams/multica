#!/usr/bin/env bash
# Fail deploy when object storage is enabled without static credentials.
#
# LRM-766: Aliyun OSS cutover set S3_BUCKET but a backend recreate without
# AWS_ACCESS_KEY_* in the Compose environment made the AWS SDK fall back to
# EC2 IMDS (unavailable on this host) → upload/presign 500s.
#
# Input must be the data-only output of `docker compose config --environment`
# (same contract as compose-environment-value.sh). Never eval/source dotenv.
set -euo pipefail

bucket=
access_key=
secret_key=

while IFS= read -r line || [[ -n "$line" ]]; do
  case "$line" in
    S3_BUCKET=*)
      bucket=${line#*=}
      ;;
    AWS_ACCESS_KEY_ID=*)
      access_key=${line#*=}
      ;;
    AWS_SECRET_ACCESS_KEY=*)
      secret_key=${line#*=}
      ;;
  esac
done

if [[ -z "$bucket" ]]; then
  exit 0
fi

if [[ -z "$access_key" || -z "$secret_key" ]]; then
  echo "S3_BUCKET is set (${bucket}) but AWS_ACCESS_KEY_ID and/or AWS_SECRET_ACCESS_KEY are empty in the rendered Compose environment." >&2
  echo "For Aliyun/dev, inject them as ambient deploy secrets (deploy.yml + docker-compose.selfhost.yml + docker-compose.oss.yml). Do not recreate backend without those keys." >&2
  exit 1
fi
