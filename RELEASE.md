# Releasing superfind 0.9.0

The morning checklist. Do the steps in order. Every command runs from the repo checkout unless it says otherwise, and every command is safe to copy as is.

```sh
cd ~/src/tries/2026-10-09-claude-sessions-search
```

## 0. Before anything else: fix the signing certificate secret

The signing check on PR #6 builds all four binaries, then stops at signing with:

```
unable to decode p12 file: pkcs12: trailing data found
```

All five signing secrets are set. `MACOS_SIGN_P12` decodes from base64, but the bytes it decodes to are not a clean `.p12` file: there is data after the end of the certificate. goreleaser reads the value with Go's `software.sslmate.com/src/go-pkcs12`, which refuses that. Nothing ships until this is fixed, because `release.yml` signs every release.

Find the `.p12` you exported from Keychain Access (call it `Certificates.p12` below), then:

1. Check that OpenSSL opens it. It asks for the `.p12` password.

   ```sh
   openssl pkcs12 -in Certificates.p12 -noout && echo opens
   ```

   If OpenSSL 3 says `unsupported` or `RC2-40-CBC`, add `-legacy` to that command and to the first `openssl` in step 2.

2. Write a clean copy. The first prompt is the old password, the second is the password for the new file; use the same one, or update `MACOS_SIGN_PASSWORD` to match.

   ```sh
   openssl pkcs12 -in Certificates.p12 -nodes | openssl pkcs12 -export -out superfind-signing.p12
   ```

3. Sign with it locally, through the same goreleaser code CI uses. This signs only; it does not notarize and does not publish anything.

   ```sh
   read -rs "P?.p12 password: "; echo
   MACOS_SIGN_P12=$(base64 -i superfind-signing.p12) MACOS_SIGN_PASSWORD=$P \
   MACOS_NOTARY_ISSUER_ID= MACOS_NOTARY_KEY_ID= MACOS_NOTARY_KEY= \
     goreleaser release --snapshot --clean
   unset P
   codesign -dv --verbose=4 dist/superfind_darwin_arm64_v8.0/superfind 2>&1 | grep -E 'Authority=Developer ID|flags|Timestamp'
   ```

   You want `Authority=Developer ID Application: …`, `flags=0x10000(runtime)`, and a `Timestamp=` line. goreleaser logs `will not try to notarize` here, which is expected.

4. Replace the secret, then run the signing check again on PR #6.

   ```sh
   base64 -i superfind-signing.p12 | gh secret set MACOS_SIGN_P12
   gh run rerun $(gh run list -b release -w signing -L1 --json databaseId --jq '.[0].databaseId')
   gh run watch $(gh run list -b release -w signing -L1 --json databaseId --jq '.[0].databaseId') --exit-status
   ```

   The `signing` workflow signs and notarizes a snapshot, then, on an Apple Silicon and an Intel Mac, checks the Developer ID authority, the hardened runtime, the timestamp, that Gatekeeper reports `source=Notarized Developer ID`, and that the binary runs with a quarantine attribute set. Green means the same signing will work for the release.

5. Store `superfind-signing.p12` as described in [Keep the keys](#keep-the-keys), then delete the loose copies from your disk.

## 1. Merge the pull requests, in this order

Each one is based on the one before, so merge them in this order and wait for each to finish before the next.

1. `superfind` (#5): the rename.
2. `qa`: the QA fixes.
3. `release` (#6): signing, notarizing, and this checklist. Its base becomes `main` when #5 merges.
4. `docs`: the README and site.

```sh
gh pr merge 5 --squash --delete-branch
gh pr list --state open    # find the qa PR number, then:
gh pr merge <qa> --squash --delete-branch
gh pr ready 6 && gh pr merge 6 --squash --delete-branch
gh pr merge <docs> --squash --delete-branch
git switch main && git pull --ff-only
```

If GitHub says a PR is out of date with its base after the one before merged, update it first with `gh pr update-branch <number>`, wait for its checks, then merge.

## 2. Make the repo public

```sh
gh repo edit adammiribyan/superfind --visibility public --accept-visibility-change-consequences
gh repo view adammiribyan/superfind --json visibility --jq .visibility
```

The description and topics are already set.

## 3. Create the Homebrew tap

goreleaser writes `Casks/superfind.rb` into this repo on every release. It must exist, be public, and have a first commit.

```sh
gh repo create adammiribyan/homebrew-tap --public --add-readme --description "Homebrew casks for superfind"
```

This is what makes `brew install --cask adammiribyan/tap/superfind` work: Homebrew turns `adammiribyan/tap` into `github.com/adammiribyan/homebrew-tap`.

## 4. Give the release workflow a token for the tap

`GITHUB_TOKEN` can only write to `superfind`, so goreleaser needs a token that can push to `homebrew-tap`.

1. Open https://github.com/settings/personal-access-tokens/new
2. Token name: `superfind release: homebrew-tap`
3. Expiration: 1 year. Put a reminder in your calendar a week before it ends; the release fails at its first step when this secret is gone, so an expired token can't ship anything half done, but it will block a release.
4. Resource owner: `adammiribyan`
5. Repository access: **Only select repositories** → `adammiribyan/homebrew-tap`
6. Permissions → Repository permissions → **Contents: Read and write**. Metadata: Read-only is added by itself. Nothing else.
7. Generate, copy, then:

```sh
gh secret set HOMEBREW_TAP_TOKEN -R adammiribyan/superfind    # paste the token at the prompt
gh secret list -R adammiribyan/superfind
```

The list should show six secrets: `HOMEBREW_TAP_TOKEN` and the five `MACOS_*` ones.

## 5. Settings that need a public repo

Turn on private vulnerability reporting, so people can report a security problem without opening a public issue:

```sh
gh api -X PUT repos/adammiribyan/superfind/private-vulnerability-reporting
gh api repos/adammiribyan/superfind/private-vulnerability-reporting --jq .enabled
```

Protect `main`: changes arrive through pull requests whose tests pass, and nobody can force-push or delete it. As the admin you can still push directly in an emergency.

```sh
gh api -X PUT repos/adammiribyan/superfind/branches/main/protection --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["test (ubuntu-latest)", "test (macos-latest)"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": null,
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

The `signing` check is not required on purpose: it runs only on pull requests that change the release pipeline, so requiring it would block every other pull request.

## 6. Upload the social preview (by hand; GitHub has no API for it)

1. Open https://github.com/adammiribyan/superfind/settings
2. General → Social preview → Edit → Upload an image.
3. Use a 1280×640 PNG under 1 MB.

There is no 1280×640 image in `~/Downloads/superfind-mark` yet: the lockup there is 1540×260, which GitHub would crop badly. Make one first, or skip this step and do it later; nothing else depends on it.

## 7. Tag v0.9.0

`release.yml` refuses a tag that doesn't match `superfind --version`, so check first:

```sh
git switch main && git pull --ff-only
go run . --version          # must print: superfind 0.9.0
git tag -a v0.9.0 -m "superfind 0.9.0"
git push origin v0.9.0
```

The last tag on GitHub is `v0.7.3`; 0.8.0 and 0.8.1 were never tagged. So the release notes goreleaser writes for v0.9.0 list everything since v0.7.3. Edit them on the release page afterwards if you want a shorter list.

## 8. Watch the release

```sh
sleep 10
gh run watch $(gh run list -w release -L1 --json databaseId --jq '.[0].databaseId') --exit-status
```

What it does, in order:

1. Stops if any of the six secrets is empty, before it builds or publishes anything.
2. Stops if the tag doesn't match `superfind --version`.
3. Builds the four binaries, signs the two darwin ones with the Developer ID certificate, sends them to Apple, and waits up to 20 minutes for notarization. Notarizing usually takes a few minutes.
4. Publishes the GitHub release and pushes `Casks/superfind.rb` to `homebrew-tap`.
5. Downloads the darwin archives from the release on an Apple Silicon and an Intel Mac, and checks each binary as in step 0.4.

If step 5 fails, the release is already public. goreleaser logs a notarization timeout without failing, and step 5 is what catches it. Apple often finishes later anyway; rerun the job after 15 minutes with `gh run rerun --failed <run id>`. If it still fails, delete the release and the tag (`gh release delete v0.9.0 --cleanup-tag --yes`), revert the cask commit in `homebrew-tap`, fix the cause, and tag again.

## 9. Check the install paths yourself

Homebrew, on your Mac. There must be no "cannot be opened" or "Apple could not verify" dialog:

```sh
brew update
brew install --cask adammiribyan/tap/superfind
superfind --version                                              # superfind 0.9.0, with no dialog
xattr -p com.apple.quarantine "$(readlink -f "$(command -v superfind)")"   # shows the quarantine mark, so Gatekeeper did check it
spctl --assess --type open --context context:primary-signature -vv "$(readlink -f "$(command -v superfind)")"   # source=Notarized Developer ID
```

Go, in a clean home, so nothing from your setup helps it:

```sh
h=$(mktemp -d)
HOME=$h go install github.com/adammiribyan/superfind@v0.9.0
HOME=$h "$h/go/bin/superfind" --version                         # superfind 0.9.0
rm -rf "$h"
```

If Go says it can't find `v0.9.0`, the module proxy hasn't seen the tag yet; wait a few minutes, or add `GOPROXY=direct`. After that, `@latest` works too.

## Keep the keys

Three files sign and notarize every release. None of them belongs in this repo, in `~/Downloads`, or in a chat.

| File | What it is | If you lose it |
| --- | --- | --- |
| `superfind-signing.p12` and its password | Developer ID Application certificate with its private key | Revoke it and make a new one at https://developer.apple.com/account/resources/certificates/list. Binaries already shipped stay valid, because each signature carries a timestamp. |
| `AuthKey_<key id>.p8` | App Store Connect API key used to notarize | Apple lets you download it only once. Revoke it and make a new one at https://appstoreconnect.apple.com/access/integrations/api, then update `MACOS_NOTARY_KEY` and `MACOS_NOTARY_KEY_ID`. |
| The fine-grained token | Lets the release push to `homebrew-tap` | Make a new one as in step 4. |

Keep the `.p12`, its password, and the `.p8` in your password manager as attached files (for example one 1Password item named "superfind release signing" holding both files, the password, the key ID, and the issuer ID). Keychain Access on your Mac also holds the certificate and its private key; that copy is enough to export the `.p12` again.

The Developer ID Application certificate is valid for 5 years from the day Apple issued it. To see the exact date:

```sh
openssl pkcs12 -in superfind-signing.p12 -nokeys | openssl x509 -noout -subject -enddate
```

Put a reminder in your calendar a month before that date. To renew, make a new Developer ID Application certificate, export it from Keychain Access as a `.p12`, then do steps 0.3 and 0.4 with the new file. Binaries signed before the certificate expires keep running after it expires.

## The pieces, for later

- `.goreleaser.yaml`: builds, signs and notarizes the darwin binaries (with [quill](https://github.com/anchore/quill), so it runs on Linux), packs the archives, and writes the cask. A real release always signs. A `--snapshot` signs only when `MACOS_SIGN_P12` is set, so `goreleaser release --snapshot --clean` works on any machine.
- `.github/workflows/release.yml`: runs on a `v*` tag. Steps as in section 8.
- `.github/workflows/signing.yml`: signs and notarizes a snapshot without publishing, and checks it on both Mac architectures. It runs on pull requests that change the release pipeline, and by hand with `gh workflow run signing.yml` once it is on `main`. The signed archives stay on the run for 7 days.
- `.github/workflows/verify-darwin.sh`: the check both workflows run on each darwin binary.
- goreleaser is pinned to v2.18.3 in both workflows. Before you move it, read its release notes for changes to notarizing.
