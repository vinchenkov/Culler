#!/bin/sh
# Source operations must preserve an incomplete public update.

culler_source_update_refusal() {
  culler_guard_width=${COLUMNS:-80}
  case "$culler_guard_width" in ''|*[!0-9]*|????*) culler_guard_width=80 ;; esac
  [ "$culler_guard_width" -ge 40 ] || culler_guard_width=40
  [ "$culler_guard_width" -le 80 ] || culler_guard_width=80
  printf '%s\n' "$1" 'Source deployment remains unchanged.' 'Use culler update to recover.' |
    fold -s -w "$culler_guard_width" >&2
  return 1
}

culler_source_update_json_safe() {
  [ -f "$1" ] && [ ! -L "$1" ] && command -v plutil >/dev/null 2>&1 || return 1
  case "$(uname -s)" in
    Darwin) culler_guard_owner=$(stat -f '%u %Lp %l' "$1" 2>/dev/null) || culler_guard_owner= ;;
    *) culler_guard_owner=$(stat -c '%u %a %h' "$1" 2>/dev/null) || culler_guard_owner= ;;
  esac
  [ "$culler_guard_owner" = "$(id -u) 600 1" ] &&
    plutil -convert json -o /dev/null "$1" >/dev/null 2>&1
}

culler_require_source_update_idle() {
  culler_guard_home=$1
  if [ -d "$culler_guard_home" ]; then
    culler_guard_home=$(CDPATH= cd -- "$culler_guard_home" && pwd -P) || return 1
  fi
  culler_guard_record=$culler_guard_home/updates/transaction.json
  if [ ! -e "$culler_guard_record" ] && [ ! -L "$culler_guard_record" ]; then return 0; fi
  if ! culler_source_update_json_safe "$culler_guard_record"; then
    culler_source_update_refusal 'Culler cannot verify the update record.'; return 1
  fi
  culler_guard_version=$(plutil -extract version raw -o - "$culler_guard_record" 2>/dev/null) || culler_guard_version=
  culler_guard_phase=$(plutil -extract phase raw -o - "$culler_guard_record" 2>/dev/null) || culler_guard_phase=
  if [ "$culler_guard_version" != 1 ]; then
    culler_source_update_refusal 'Culler cannot verify the update record.'; return 1
  fi
  case "$culler_guard_phase" in
    complete|restored) ;;
    stop|prepare|checkpoint|replace|verify|committed|restore)
      culler_source_update_refusal 'Culler has an incomplete update.'; return 1 ;;
    *) culler_source_update_refusal 'Culler cannot verify the update record.'; return 1 ;;
  esac
  culler_guard_request=$culler_guard_home/state/install-maintenance.json
  if [ ! -e "$culler_guard_request" ] && [ ! -L "$culler_guard_request" ]; then return 0; fi
  if ! culler_source_update_json_safe "$culler_guard_request"; then
    culler_source_update_refusal 'Culler cannot verify the update record.'; return 1
  fi
  culler_guard_id=$(plutil -extract id raw -o - "$culler_guard_record" 2>/dev/null) || culler_guard_id=
  culler_guard_request_id=$(plutil -extract id raw -o - "$culler_guard_request" 2>/dev/null) || culler_guard_request_id=
  case "$culler_guard_id" in
    ''|*[!a-f0-9]*) culler_source_update_refusal 'Culler cannot verify the update record.'; return 1 ;;
  esac
  case "$culler_guard_request_id" in
    ''|*[!a-zA-Z0-9-]*) culler_source_update_refusal 'Culler cannot verify the update record.'; return 1 ;;
  esac
  if [ "${#culler_guard_id}" -lt 16 ] || [ "${#culler_guard_id}" -gt 64 ] ||
      [ "${#culler_guard_request_id}" -lt 16 ] || [ "${#culler_guard_request_id}" -gt 128 ]; then
    culler_source_update_refusal 'Culler cannot verify the update record.'; return 1
  fi
  if [ "$culler_guard_id" = "$culler_guard_request_id" ]; then
    culler_source_update_refusal 'Culler has an incomplete update.'; return 1
  fi
}
