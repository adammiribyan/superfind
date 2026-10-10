#!/bin/sh
set -eu

bin=$1
want=$2

info=$(codesign -dv --verbose=4 "$bin" 2>&1)
echo "$info"
echo "$info" | grep -q '^Authority=Developer ID Application: ' || { echo "not signed with a Developer ID Application certificate" >&2; exit 1; }
echo "$info" | grep -Eq '^CodeDirectory .*flags=.*runtime' || { echo "hardened runtime is off" >&2; exit 1; }
echo "$info" | grep -q '^Timestamp=' || { echo "no secure timestamp" >&2; exit 1; }
codesign --verify --strict --verbose=2 "$bin"

assess=$(spctl --assess --type open --context context:primary-signature -vv "$bin" 2>&1) || { echo "$assess" >&2; exit 1; }
echo "$assess"
echo "$assess" | grep -q '^source=Notarized Developer ID$' || { echo "Gatekeeper does not see a notarization ticket" >&2; exit 1; }

xattr -w com.apple.quarantine "0081;$(printf %x "$(date +%s)");Safari;" "$bin"
xattr -p com.apple.quarantine "$bin"
got=$("$bin" --version)
echo "$got"
test "$got" = "$want" || { echo "want $want, got $got" >&2; exit 1; }
