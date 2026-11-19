# Publishing to Forgejo and GHCR

`.forgejo/workflows/docker-publish.yml` builds the image from Forgejo `main`, then
publishes both `forgejo.jwala.net/bheem/calnode` and
`ghcr.io/bheem1224/calnode`, each with `latest` and commit-SHA tags. The
GitHub package owner must match the GHCR namespace. This Forgejo workflow
currently builds `linux/amd64`; use an x86-64 Azure VM for this image.

## One-time setup

1. Keep the Forgejo Actions `REGISTRY_TOKEN` secret with package-write access
   to `forgejo.jwala.net/bheem/calnode`. The Forgejo login username is the
   Forgejo workflow actor (`github.actor`), as in the original publishing flow.
2. On GitHub, create a **personal access token (classic)** owned by the account
   allowed to publish `ghcr.io/bheem1224/calnode`. Grant `write:packages`. The
   token is only for GHCR publication; Forgejo's checkout uses its own token.
   `repo`, `read:user`, and `delete:packages` are not required for this public
   container package. GitHub's ordinary token form automatically selects
   `repo` with `write:packages`; use GitHub's
   [write:packages-only token form](https://github.com/settings/tokens/new?scopes=write:packages)
   to avoid that broad scope. See [GitHub's container registry instructions](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
3. In Forgejo Actions secrets, set `GHCR_USERNAME` to the GitHub token owner's
   **account username** (for this repository, `bheem1224`, not the email used
   to sign in) and `GHCR_TOKEN` to that classic token. Never place the token
   in the workflow, Compose file, or a VM environment file.
4. Run the workflow. After the first successful push, open the GitHub `calnode`
   package settings and change its visibility to **Public** if anonymous VM
   pulls are intended. Packages under a personal account start private; a
   public GHCR container can be pulled without Docker login. This visibility
   change cannot be reversed to private. See [package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).
5. On the VM, pull `ghcr.io/bheem1224/calnode:<commit-sha>` or pin its digest.
   `latest` is convenient for the first run but moves with each successful
   Forgejo `main` publish. Check `/version` after deploying to confirm the
   running binary reports the intended commit.

The Forgejo runner needs outbound HTTPS to both registries. The VM needs
outbound HTTPS to GHCR and no Forgejo registry credentials once the GHCR
package is public.
The workflow's separate build and publish steps make a registry rejection
distinguishable from a compilation failure; successful pushes to both registries
are the final publication checks. This is a separate Forgejo workflow from `ci.yml`; a
successful Docker build does not by itself mean the CI test job passed.
