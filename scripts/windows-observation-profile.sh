#!/usr/bin/env bash
# Optional OS evidence for the original strict tests on disposable Windows CI.
# Source this helper; diagnostics never replace the test's exit status.

observation_profile_start() {
  AN_OBSERVATION_PROFILE_STARTED=0
  AN_OBSERVATION_PROFILE_PHASE="$1"
  AN_OBSERVATION_PROFILE_INSTANCE="AgentNotifications-${GITHUB_RUN_ID:-unknown}-${GITHUB_RUN_ATTEMPT:-unknown}-$1"
  if [[ "${GITHUB_ACTIONS:-}" != true || "${RUNNER_OS:-}" != Windows ]]; then
    return 0
  fi
  if ! command -v wpr.exe >/dev/null 2>&1; then
    printf 'WPR unavailable\n' > "observation-evidence/$1-os-start.log"
    return 0
  fi
  # Memory mode is bounded. A named instance prevents stopping another recorder.
  if wpr.exe -start GeneralProfile -start FileIO -start Minifilter -instancename "$AN_OBSERVATION_PROFILE_INSTANCE" \
      > "observation-evidence/$1-os-start.log" 2>&1; then
    AN_OBSERVATION_PROFILE_STARTED=1
  fi
  return 0
}

observation_profile_stop() {
  if [[ "${AN_OBSERVATION_PROFILE_STARTED:-0}" != 1 ]]; then
    return 0
  fi
  # Attempt stop once, only for the successfully started instance. Never cancel
  # other sessions or change antivirus/driver policy to obtain a trace.
  AN_OBSERVATION_PROFILE_STARTED=0
  if wpr.exe -stop "observation-evidence/$AN_OBSERVATION_PROFILE_PHASE-os.etl" \
    -skipPdbGen -compress -instancename "$AN_OBSERVATION_PROFILE_INSTANCE" \
    > "observation-evidence/$AN_OBSERVATION_PROFILE_PHASE-os-stop.log" 2>&1; then
    if [[ -s "observation-evidence/$AN_OBSERVATION_PROFILE_PHASE-os.etl" ]]; then
      printf 'OS profile %s retained\n' "$AN_OBSERVATION_PROFILE_PHASE"
    else
      printf 'OS profile %s empty; see recorder logs\n' "$AN_OBSERVATION_PROFILE_PHASE"
    fi
  else
    printf 'OS profile %s unavailable; see recorder logs\n' "$AN_OBSERVATION_PROFILE_PHASE"
  fi
  return 0
}
