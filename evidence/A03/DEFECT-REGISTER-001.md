# Defect register 001 — redeploy.sh stops launchd units from the plist list, not the loaded list

Status: REGISTERED, NOT FIXED. The operator ruled on 2026-11-20 that this latent
defect is registered as its own record while A03 adds the systemd branch, and
that no A03 change may silently alter the Darwin stop behavior it lives in.
Nothing in this file is a claim that the code was repaired.

## Location

`redeploy.sh`, the `== stop: supervised units` section, the Darwin branch: the
loop `for plist in "$HOME/Library/LaunchAgents"/com.culler.*.plist`. Before the
A03 seam it was the only stop loop; after the seam it is the branch taken when
the service family is Darwin.

## The mismatch

Two different sources of truth about which Culler services run:

- The stop loop iterates the plist FILES in `~/Library/LaunchAgents`.
- `units_loaded` (used by the early database messages and by the bounded wait
  loop before the image build) iterates the live OUTPUT of `launchctl list`.

A loaded unit whose plist no longer sits in that directory is therefore never
booted out: the loop never names its label. The wait loop then polls a unit
that never leaves, exhausts its 50 polls, and the script stops with

    redeploy.sh: a supervised Culler deployment is loaded.

The deployment is wedged from `redeploy.sh`'s point of view until the operator
restores the plist or boots the unit out by hand. The same asymmetry in the
other direction (a plist whose label is not loaded) is already handled: the
`launchctl print` inspection recognizes exit 113 and reports "already stopped".

## Why it stays open

- The failure is loud and conservative: the script refuses before changing the
  image, the executable, or the database. No wrong release is deployed.
- Fixing it means driving the Darwin stop from `launchctl list` instead of the
  plist glob. That changes the observable call transcript the existing service
  cases pin (the bootout race and the absent-service case), so it needs its own
  ticket with its own test revisions, not a slip-in during A03.

## What A03 proves about it

The Linux branch added in A03 does not inherit the defect: its stop loop
enumerates the names `systemctl --user list-units` reports, which is the same
source the wait loop checks. The Linux happy-path case in `redeploy_test.sh`
pins the list-driven order (dashboard unit disabled before the resident unit,
then the wait poll sees an empty list).

Severity: P2 — an availability wedge under a precondition the deployment
itself never creates (a hand-removed plist), failing closed with a clear
message.
