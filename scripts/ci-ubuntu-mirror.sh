#!/usr/bin/env bash
# Use an Ubuntu archive mirror only when the hosted runner cannot resolve its
# configured Azure mirror. Apt's normal Ubuntu signature checks stay enabled.
set -euo pipefail

if getent ahosts azure.archive.ubuntu.com >/dev/null 2>&1; then
  exit 0
fi

if ! { grep -qsF 'mirror+file:/etc/apt/apt-mirrors.txt' /etc/apt/sources.list ||
       grep -RqsF 'mirror+file:/etc/apt/apt-mirrors.txt' /etc/apt/sources.list.d; }; then
  echo 'Ubuntu mirror fallback unavailable: unexpected apt sources' >&2
  exit 1
fi
if ! getent ahosts mirrors.edge.kernel.org >/dev/null 2>&1 ||
   ! curl --fail --silent --show-error --head --location --connect-timeout 10 --max-time 20 \
      https://mirrors.edge.kernel.org/ubuntu/dists/jammy/InRelease >/dev/null; then
  echo 'Ubuntu mirror fallback unavailable' >&2
  exit 1
fi
echo 'Azure Ubuntu mirror DNS unavailable; using signed Ubuntu archive via kernel.org mirror.'
printf '%s\n' 'https://mirrors.edge.kernel.org/ubuntu' | sudo tee /etc/apt/apt-mirrors.txt >/dev/null
