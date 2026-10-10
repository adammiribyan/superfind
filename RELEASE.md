# Releasing superfind 0.9.0

The morning checklist. Do the steps in order. Commands run in zsh from the main checkout unless a step says otherwise:

```sh
cd ~/src/tries/2026-10-09-claude-sessions-search
```

## 0. Before anything else: get Actions running, and reset the signing certificate secret

**Done on 2026-10-10.** The Actions spending limit was raised, `MACOS_SIGN_P12` was reset at 07:48:36Z, and signing run 38004434376 signed and notarized both darwin binaries and verified them on both Mac architectures. Skip to step 1. The rest of this section stays here for the next time the certificate secret needs resetting.

### 0a. Actions billing

Since about 23:04Z on 2026-10-09, GitHub has refused to start every Actions job on this repo with: "The job was not started because recent account payments have failed or your spending limit needs to be increased." Until that is fixed, no check runs: not the tests, not the signing check, not the release. Fix it one of two ways:

- Fix the payment or raise the Actions spending limit at https://github.com/settings/billing. A private repo pays for every minute, and macOS minutes cost 10 times Linux ones; the signing check and the release each run two macOS jobs.
- Or make the repo public now (step 2 below). Actions is free for public repos. Going public before the merges means people see `main` as it is now, still named hop in places; that is your call.

Check that a job really starts: push nothing, just rerun the newest test run and watch it:

```sh
run=$(gh run list -L1 --json databaseId --jq '.[0].databaseId')
gh run rerun "$run" && sleep 20 && gh run view "$run" --json jobs --jq '.jobs[] | "\(.name): \(.status) \(.conclusion)"'
```

A job that shows `in_progress` or `completed success` started. A job that failed within seconds with no steps did not; `gh run view "$run" --log-failed` is empty then, and the billing message is in the run's annotations on the web page.

### 0b. The signing certificate secret


The signing check on PR #6 builds all four binaries, then stops at signing with:

```
unable to decode p12 file: pkcs12: trailing data found
```

The cause: `MACOS_SIGN_P12` was set by piping `op read` into `base64`, and `op read` adds a newline to its output, so one stray byte follows the certificate. OpenSSL ignores that byte; Go's `pkcs12`, which goreleaser uses, refuses it. `--out-file` writes the file's exact bytes instead. Unlock 1Password, then:

```sh
op read --account my.1password.com --out-file /tmp/superfind.p12 "op://Private/superfind Developer ID .p12 export/superfind.p12" \
  && base64 -i /tmp/superfind.p12 | gh secret set MACOS_SIGN_P12 -R adammiribyan/superfind \
  && rm /tmp/superfind.p12
gh secret list -R adammiribyan/superfind | grep MACOS_SIGN_P12    # the date should be today
```

That's all that's needed. When the secret's date moves, the release session reruns the signing check on PR #6 and records the proof there, then marks #6 ready. To rerun it yourself:

```sh
run=$(gh run list -b release -w signing -L1 --json databaseId --jq '.[0].databaseId')
gh run rerun "$run" && sleep 5 && gh run watch "$run" --exit-status
```

The `signing` workflow signs and notarizes a snapshot. Then, on an Apple Silicon and an Intel Mac, it checks for the Developer ID authority, the hardened runtime, and the timestamp; checks that Gatekeeper reports `source=Notarized Developer ID`; and runs the binary with a quarantine attribute set. Green means the release will sign the same way.

If it still fails on the certificate, sign locally through the same goreleaser code to see the problem on your own machine. This signs only; it does not notarize or publish:

```sh
op read --account my.1password.com --out-file /tmp/superfind.p12 "op://Private/superfind Developer ID .p12 export/superfind.p12"
cd ~/src/tries/2026-10-09-claude-sessions-search-release
read -rs "P?.p12 password: "; echo
MACOS_SIGN_P12=$(base64 -i /tmp/superfind.p12) MACOS_SIGN_PASSWORD=$P \
MACOS_NOTARY_ISSUER_ID= MACOS_NOTARY_KEY_ID= MACOS_NOTARY_KEY= \
  goreleaser release --snapshot --clean
unset P; rm /tmp/superfind.p12
codesign -dv --verbose=4 dist/superfind_darwin_arm64_v8.0/superfind 2>&1 | grep -E 'Authority=Developer ID|flags=|Timestamp='
cd -
```

You want `Authority=Developer ID Application: … (25B5ZUUCLF)`, `flags=0x10000(runtime)`, and a `Timestamp=` line. goreleaser logs `will not try to notarize` here; that's expected.

## 1. Merge the pull requests, in this order

1. `superfind`: the rename (#5, into `main`).
2. `qa`: the QA fixes.
3. `release`: signing, notarizing, and this checklist (#6, currently based on `superfind`).
4. `docs`: the README and site.

When this was written, only #5 and #6 were open; `qa` and `docs` existed only as local branches. Look first:

```sh
gh pr list --state open --json number,headRefName,baseRefName --jq '.[] | "#\(.number) \(.headRefName) -> \(.baseRefName)"'
```

Merge each PR only when its checks are green and its base is `main`. When a PR's base branch is deleted after merging, GitHub moves the PRs stacked on it onto `main`. If GitHub then says the PR is out of date or has conflicts, run `gh pr update-branch <number>`, wait for the checks, and merge.

Don't pass `--delete-branch` to `gh pr merge`: these branches are checked out in sibling worktrees, so the local delete fails and `gh` exits with an error after the merge went through. Delete the remote branch yourself instead:

```sh
gh pr merge <number> --squash
git push origin --delete <branch>
```

`#6` is a draft; mark it ready first with `gh pr ready 6`. When all four are merged:

```sh
git switch main && git pull --ff-only
for b in superfind qa release docs; do git worktree remove "../2026-10-09-claude-sessions-search-$b"; git branch -D "$b"; done
```

`git worktree remove` refuses if a worktree has uncommitted changes. If it does, look at those changes before you do anything else.

## 2. Make the repo public

```sh
gh repo edit adammiribyan/superfind --visibility public --accept-visibility-change-consequences
gh repo view adammiribyan/superfind --json visibility --jq .visibility
```

These are already set:

- The repo description: "Pick up a past Claude Code, Codex, or Grok session from what you remember of it."
- 13 topics.
- The cask's `desc` in `.goreleaser.yaml`: "Terminal picker for resuming Claude Code, Codex, and Grok sessions". It has no trailing period, and `brew style` passes on the generated cask.

## 3. Create the Homebrew tap

The release writes `Casks/superfind.rb` into this repo. It must exist, be public, and have a first commit:

```sh
gh repo create adammiribyan/homebrew-tap --public --add-readme --description "Homebrew casks for superfind"
```

Homebrew turns `adammiribyan/tap` into `github.com/adammiribyan/homebrew-tap`, which is what makes `brew install --cask adammiribyan/tap/superfind` work.

## 4. Give the release a token for the tap

The workflow's own token can only write to `superfind`, so the release needs a token that can write to `homebrew-tap`.

1. Open https://github.com/settings/personal-access-tokens/new
2. Token name: `superfind release: homebrew-tap`
3. Expiration: 1 year. Add a calendar reminder a week before it ends. An expired token stops the release at its second step, before anything is built.
4. Resource owner: `adammiribyan`
5. Repository access: **Only select repositories** → `adammiribyan/homebrew-tap`
6. Permissions → Repository permissions → **Contents: Read and write**. GitHub adds Metadata: Read-only by itself. Nothing else.
7. Generate it, copy it, then:

```sh
gh secret set HOMEBREW_TAP_TOKEN -R adammiribyan/superfind    # paste the token at the prompt
gh secret list -R adammiribyan/superfind
```

You should see six secrets: `HOMEBREW_TAP_TOKEN` and the five `MACOS_*` ones.

## 5. Settings that need a public repo

Private vulnerability reporting, so people can report a security problem without opening a public issue:

```sh
gh api -X PUT repos/adammiribyan/superfind/private-vulnerability-reporting
gh api repos/adammiribyan/superfind/private-vulnerability-reporting --jq .enabled    # true
```

Branch protection on `main`. Changes must arrive through a pull request whose tests pass (no approving review required, since you work alone), and nobody can force-push to `main` or delete it. As the admin you can still bypass this in an emergency.

```sh
gh api -X PUT repos/adammiribyan/superfind/branches/main/protection --input - <<'EOF'
{
  "required_status_checks": {
    "strict": true,
    "contexts": ["test (ubuntu-latest)", "test (macos-latest)"]
  },
  "enforce_admins": false,
  "required_pull_request_reviews": { "required_approving_review_count": 0 },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false
}
EOF
```

The `signing` check is not required on purpose. It runs only on pull requests that change the release pipeline, so requiring it would block every other pull request.

## 6. Upload the social preview (by hand; GitHub has no API for it)

1. Open https://github.com/adammiribyan/superfind/settings
2. General → Social preview → Edit → Upload an image.
3. Use a 1280×640 PNG under 1 MB.

There is no 1280×640 image in `~/Downloads/superfind-mark` yet. The lockup there is 1540×260, and GitHub would crop it badly. Make one first, or do this step later; nothing else depends on it.

## 7. Tag v0.9.0

Your clone has three tags that were never pushed: `v0.8.0`, `v0.8.1`, and an old `v0.9.0` on `29ebbe0`, the commit from before the rename. If you push that `v0.9.0`, its old workflow publishes unsigned `hop` binaries. Delete all three, then tag the merged `main`:

```sh
git switch main && git pull --ff-only
git tag -d v0.8.0 v0.8.1 v0.9.0
test -z "$(git ls-remote --tags origin v0.9.0)" && echo "no v0.9.0 on GitHub yet"
go run . --version                                   # must print: superfind 0.9.0
git tag -a v0.9.0 -m "superfind 0.9.0" && \
  test "$(git rev-parse 'v0.9.0^{commit}')" = "$(git rev-parse origin/main)" && \
  git push origin v0.9.0
```

The `&&` chain pushes only when the new tag exists and points at the merged `main`. Never run `git push --tags` in this repo.

The last tag on GitHub is `v0.7.3`, so the release notes goreleaser writes list everything since 0.7.3. You can shorten them on the release page afterwards.

## 8. Watch the release

```sh
sleep 15
run=$(gh run list -w release -b v0.9.0 -L1 --json databaseId --jq '.[0].databaseId')
gh run watch "$run" --exit-status
```

`release.yml` runs three jobs. No release and no cask reach users until the last one:

1. **build**: stops if any of the six secrets is empty, if the tap token can't reach `homebrew-tap`, if `v0.9.0` is already published, or if the tag doesn't match `superfind --version`. Then it builds the four binaries, signs the two darwin ones, sends them to Apple, waits up to 20 minutes for notarization (usually a few minutes), and uploads everything to a **draft** release that only you can see.
2. **verify**: on an Apple Silicon and an Intel Mac, downloads the darwin archive from the draft and checks it the same way as in step 0.
3. **publish**: pushes `Casks/superfind.rb` to `homebrew-tap`, then publishes the draft as the latest release. Rerunning it is safe: if the release is already published, it stops there.

If **build** or **verify** fails, nothing is published. The usual cause is that Apple was slow: goreleaser only logs a notarization timeout, and **verify** is what catches it. Apple usually finishes within the hour. Rerun the failed jobs then:

```sh
gh run rerun "$run" --failed
```

If the code itself must change, don't move the `v0.9.0` tag. Go's module proxy may already have recorded it. Delete the draft (`gh release delete v0.9.0 --yes`), fix the code, set `version` to `0.9.1`, and release `v0.9.1`.

If **publish** fails after the cask push, the cask points at a release that is still a draft, so `brew install` fails until you rerun the job.

## 9. Check the install paths yourself

Homebrew, on your Mac. No "cannot be opened" or "Apple could not verify" dialog may appear:

```sh
brew update
brew install --cask adammiribyan/tap/superfind
sfbin=$(readlink -f "$(command -v superfind)")
superfind --version                     # superfind 0.9.0, and no dialog
xattr -p com.apple.quarantine "$sfbin"   # prints a quarantine value, so Gatekeeper did check it
spctl --assess --type open --context context:primary-signature -vv "$sfbin"   # source=Notarized Developer ID
```

Go, in a clean home. `env -i` matters: your shell exports `GOPATH`, which would otherwise send the install to `~/go/bin`.

```sh
h=$(mktemp -d)
env -i PATH="$PATH" HOME="$h" GOFLAGS=-modcacherw go install github.com/adammiribyan/superfind@v0.9.0
"$h/go/bin/superfind" --version         # superfind 0.9.0
rm -rf "$h"
```

If Go says it can't find `v0.9.0`, the module proxy hasn't seen the tag yet. Wait a few minutes, or add `GOPROXY=direct` inside the `env -i`. After that, `@latest` works too.

## Keep the keys

Three secrets sign, notarize, and publish every release. None of them belongs in this repo, in `~/Downloads`, or in a chat.

| What | Used for | If you lose it | If someone else may have it |
| --- | --- | --- | --- |
| The `.p12` and its password | Developer ID Application certificate and private key; signs the binaries | Export it again from Keychain Access, which keeps the key. If that copy is gone too, make a new certificate. Don't revoke the old one: binaries already shipped stay valid. | Revoke it at https://developer.apple.com/account/resources/certificates/list and make a new one. Revoking can stop already-shipped binaries from launching, so do it only for a real compromise, and ask Apple Developer Support about a revocation date that spares the good releases. |
| `AuthKey_<key id>.p8` | App Store Connect API key; notarizes | Apple lets you download it only once. Make a new key at https://appstoreconnect.apple.com/access/integrations/api, update `MACOS_NOTARY_KEY` and `MACOS_NOTARY_KEY_ID`, and revoke the old key. Revoking it affects nothing already shipped. | Same as lost. |
| The fine-grained token | Pushes the cask to `homebrew-tap` | Make a new one as in step 4. | Delete it at https://github.com/settings/personal-access-tokens, then make a new one. |

They live in the 1Password item "superfind Developer ID .p12 export" (my.1password.com, vault Private): the `.p12`, its password, the `.p8`, the key ID, and the issuer ID. Keep them there and nowhere else.

A Developer ID Application certificate is valid for 5 years from the day Apple issued it. This one expires on **2031-09-17**. Add a calendar reminder for 2031-08-17. To check the date from the file:

```sh
op read --account my.1password.com --out-file /tmp/superfind.p12 "op://Private/superfind Developer ID .p12 export/superfind.p12"
$(brew --prefix openssl)/bin/openssl pkcs12 -legacy -in /tmp/superfind.p12 -nokeys | $(brew --prefix openssl)/bin/openssl x509 -noout -subject -enddate
rm /tmp/superfind.p12
```
 To renew, make a new Developer ID Application certificate, export it from Keychain Access as a `.p12`, attach the new `.p12` to the 1Password item, and set `MACOS_SIGN_P12` and `MACOS_SIGN_PASSWORD` from it as in step 0. Binaries signed before the old certificate expires keep running afterwards, because each signature carries a secure timestamp.

## The pieces, for later

- `.goreleaser.yaml`: builds, signs, and notarizes the darwin binaries. It uses [quill](https://github.com/anchore/quill), so it runs on Linux. It packs the archives, writes the cask without pushing it, and uploads to a draft release. A real release always signs. A `--snapshot` signs only when `MACOS_SIGN_P12` is set, so `goreleaser release --snapshot --clean` works on any machine.
- `.github/workflows/release.yml`: runs on a `v*` tag, as described in section 8.
- `.github/workflows/signing.yml`: signs and notarizes a snapshot without publishing, and checks it on both Mac architectures. It runs on pull requests that change the release pipeline. Once it is on `main`, you can also run it by hand with `gh workflow run signing.yml`. The signed archives stay on the run for 7 days.
- `.github/workflows/verify-darwin.sh`: the check both workflows run on each darwin binary.
- Both workflows pin goreleaser to v2.18.3. Before you change that, read goreleaser's release notes for changes to notarizing.
