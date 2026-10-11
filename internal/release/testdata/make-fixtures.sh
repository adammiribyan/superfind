#!/bin/sh
set -eu
cd "$(dirname "$0")"
(cd release && rm -f checksums.txt checksums.txt.minisig && shasum -a 256 install.sh superfind_0.11.1_linux_amd64.tar.gz > checksums.txt)
minisign -S -l -W -s test.key -t "superfind v0.11.1 checksums.txt" -m release/checksums.txt
work=$(mktemp -d)
cp release/install.sh release/superfind_0.11.1_linux_amd64.tar.gz release/checksums.txt "$work"
SUPERFIND_SIGNING_KEY=$(base64 < test.key) go run ../cmd/sign -pubkey test.pub 0.11.1 "$work"
mv "$work/checksums.txt.minisig" superfind-sign.minisig
rm -r "$work"
minisign -V -m release/checksums.txt -p test.pub
minisign -V -m release/checksums.txt -x superfind-sign.minisig -p test.pub
