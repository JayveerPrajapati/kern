# Signed releases (GPG)

kern publishes **SHA256SUMS** checksums for every release, giving you
integrity (the bytes you downloaded are the bytes that were released). GPG
signing adds **authenticity**: proof that a release was produced by a key held
by the maintainers, not just anyone who can push to the tag.

GPG signing is **optional but recommended**. If the signing secrets are not
configured, the release workflow skips every signing step and publishes
checksums only — nothing in the pipeline fails.

## Current status

| Mechanism | Always published | Purpose |
|---|---|---|
| `SHA256SUMS` | yes | Integrity: `sha256sum -c SHA256SUMS` |
| `SHA256SUMS.sig` | when keys configured | Authenticity of the checksums file |
| `<asset>.sig` (per binary) | when keys configured | Authenticity of each binary |
| `SHA256SUMS.asc` | when keys configured | Public key, so you can verify without a keyserver |

`install.sh` verifies the downloaded tarball against `SHA256SUMS`. GPG signing
is layered on top of that for users who want cryptographic authenticity.

## 1. Generate a GPG key

You need a GPG keypair dedicated to release signing. On a maintainer machine
with `gpg` installed:

```sh
gpg --full-generate-key
```

- Key type: **RSA and RSA** (4096 bits) or **Ed25519**.
- No expiration (a signing key that expires silently breaks older releases);
  revoke it deliberately if it is ever compromised.
- Use a strong passphrase.

List the new key:

```sh
gpg --list-secret-keys --keyid-format=long
```

Take note of the key ID (the hex after the algorithm, e.g. `AAAAAAAAAAAAAAAA`).

Publish the public key so users can find it independently of the release
assets (recommended, so verification does not rely on the very channel you are
verifying):

```sh
gpg --keyserver keys.openpgp.org --send-keys AAAAAAAAAAAAAAAA
```

## 2. Configure the environment variables

Signing is driven by two **repository secrets** (Settings → Secrets and
variables → Actions → New repository secret). Secrets are never stored in the
repo, never appear in logs, and are only exposed to the `release` job.

### `GPG_PRIVATE_KEY`

Base64-encoded ASCII-armored export of the **private** key:

```sh
gpg --armor --export-secret-keys AAAAAAAAAAAAAAAA | base64
```

Copy the entire (single-line) output into the `GPG_PRIVATE_KEY` secret.

### `GPG_PASSPHRASE`

The passphrase you chose when generating the key. Required — CI signs
non-interactively with `--pinentry-mode loopback`.

> **Why base64?** GitHub Actions secrets must not contain raw newlines;
> base64 folds the multi-line armored block into one line.

## 3. What CI does

On every tag push, the `release` job in `.github/workflows/release.yml`:

1. **Imports** the private key from `GPG_PRIVATE_KEY` into the ephemeral
   runner keyring.
2. **Signs** `dist/SHA256SUMS` → `SHA256SUMS.sig` (armored detached
   signature), and each `kern-*.tar.gz` / `kern-*.zip` → `<asset>.sig`.
3. **Exports** the public key as `SHA256SUMS.asc`.
4. **Verifies** every signature it just produced; a failed verification fails
   the job, so a broken signature can never be uploaded.
5. **Uploads** `SHA256SUMS.sig`, `SHA256SUMS.asc`, and all `.sig` files to the
   GitHub release.

All signing steps are guarded by `if: ${{ env.GPG_PRIVATE_KEY != '' }}` —
with the secrets unset the job behaves exactly as before (checksums only).

## 4. How users verify signatures

Download the release assets and the public key:

```sh
tag=v0.9.5
base="https://github.com/<owner>/kern/releases/download/${tag}"
curl -fsSL -O "${base}/SHA256SUMS"
curl -fsSL -O "${base}/SHA256SUMS.sig"
curl -fsSL -O "${base}/SHA256SUMS.asc"
curl -fsSL -O "${base}/kern-linux-amd64.tar.gz"
```

Verify the public key (optional but recommended — fetch it from a keyserver
rather than the release itself to avoid trusting the channel under attack):

```sh
gpg --import SHA256SUMS.asc                      # or: gpg --keyserver keys.openpgp.org --recv-keys <KEYID>
gpg --fingerprint SHA256SUMS.asc                 # confirm the fingerprint matches what maintainers publish out-of-band
```

Verify the signatures:

```sh
gpg --verify SHA256SUMS.sig SHA256SUMS           # authenticates the checksums file
gpg --verify kern-linux-amd64.tar.gz.sig kern-linux-amd64.tar.gz
```

Expected output ends with:

```
gpg: Good signature from "kern release signing key <kern@example.org>"
```

Then verify the checksums as usual:

```sh
sha256sum -c SHA256SUMS
```

If `gpg` reports `BAD signature` or `Can't check signature`, stop — do not
install the binaries.

## 5. Disabling / skipping signing

Do nothing. Signing is purely additive: leave `GPG_PRIVATE_KEY` and
`GPG_PASSPHRASE` unset and every signing step is skipped, including the
verification and upload steps. The release still publishes `SHA256SUMS`, and
`install.sh` still verifies against it.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `gpg: no valid OpenPGP data found` | `GPG_PRIVATE_KEY` is not valid base64 of an armored key; re-export and re-encode |
| `gpg: decryption failed: No secret key` / signing fails | key not imported, or the wrong key ID was exported; re-check `--export-secret-keys` |
| `Bad passphrase` in CI | `GPG_PASSPHRASE` does not match the key's passphrase |
| Verification needs the public key but the release predates signing | older releases are checksum-only; verify `SHA256SUMS` and skip the signature check |
| `gpg: Can't check signature: No public key` | import `SHA256SUMS.asc` or fetch the key from a keyserver first |

## Security notes

- The private key exists only as a **secret** in GitHub Actions; it is never
  committed, and the release workflow only ever needs it inside the ephemeral
  runner keyring.
- Prefer a dedicated signing key over your personal key, and revoke it (rather
  than just deleting it) if it is ever exposed.
- A release can be GPG-signed **and** still be tampered with if the attacker
  also controls the signing key; signatures protect against a compromise of
  the release channel (e.g. the GitHub release assets), not of the key.