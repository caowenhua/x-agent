#!/usr/bin/env bash

set -euo pipefail

dist_dir="${1:-dist}"
output_dir="${2:-}"

if [[ -z "${output_dir}" ]]; then
  output_dir="$(mktemp -d)"
fi

rm -rf "${output_dir}"
mkdir -p "${output_dir}"

selected_archive=""
work_dir=""

cleanup() {
  if [[ -n "${work_dir}" && -d "${work_dir}" ]]; then
    rm -rf "${work_dir}"
  fi
}

trap cleanup EXIT

while IFS= read -r archive; do
  archive_contents="$(tar -tzf "${archive}" 2>/dev/null || true)"
  if [[ -z "${archive_contents}" ]]; then
    continue
  fi
  if ! grep -qx 'xxx-code' <<<"${archive_contents}" || ! grep -qx 'xxx-code-stability' <<<"${archive_contents}"; then
    continue
  fi

  work_dir="$(mktemp -d)"
  if ! tar -xzf "${archive}" -C "${work_dir}" xxx-code xxx-code-stability >/dev/null 2>&1; then
    rm -rf "${work_dir}"
    work_dir=""
    continue
  fi

  if [[ ! -f "${work_dir}/xxx-code" || ! -f "${work_dir}/xxx-code-stability" ]]; then
    rm -rf "${work_dir}"
    work_dir=""
    continue
  fi

  chmod +x "${work_dir}/xxx-code" "${work_dir}/xxx-code-stability"

  if "${work_dir}/xxx-code" --version >"${output_dir}/xxx-code.version.txt" 2>"${output_dir}/xxx-code.version.err" && \
    "${work_dir}/xxx-code-stability" --version >"${output_dir}/xxx-code-stability.version.txt" 2>"${output_dir}/xxx-code-stability.version.err"; then
    selected_archive="${archive}"
    printf '%s\n' "${archive}" >"${output_dir}/archive.txt"
    printf '%s\n' "${archive_contents}" >"${output_dir}/archive-contents.txt"
    break
  fi

  rm -rf "${work_dir}"
  work_dir=""
done < <(find "${dist_dir}" -maxdepth 1 -type f -name '*.tar.gz' | sort)

if [[ -z "${selected_archive}" ]]; then
  echo "no runnable release archive found in ${dist_dir}" >&2
  exit 1
fi

echo "selected release archive: ${selected_archive}"
cat "${output_dir}/xxx-code.version.txt"
cat "${output_dir}/xxx-code-stability.version.txt"
