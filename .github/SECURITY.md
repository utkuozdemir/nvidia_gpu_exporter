# Security policy

## Reporting a problem

If you find a security problem in the exporter, its container images, the Helm chart or the release pipeline, please report it privately instead of opening a public issue.
The preferred way is GitHub's private reporting form: [Report a vulnerability](https://github.com/utkuozdemir/nvidia_gpu_exporter/security/advisories/new).
If that does not work for you, email <utkuozdemir@gmail.com>.

Before reporting, see the [security model](../docs/SECURITY_MODEL.md) for what is in scope.
The metrics endpoint being unauthenticated and bound to all interfaces by default is documented behavior, not a finding on its own.

This is a side project maintained in spare time, so please allow up to two weeks for a first response.
A confirmed report is prioritized by severity, and the fix ships in a release rather than sitting on a branch.
The report is published as an advisory after the release, with credit if you want it.
Please keep the details private until then (coordinated disclosure).

## Supported versions

Only the latest release receives fixes.
There are no maintenance branches for older versions.

## Verifying what you run

The checksums file of every release is signed with GPG, which covers the binaries, archives and packages.
The container images and the Helm chart are signed keyless with cosign.
See [Verifying what you downloaded](../docs/INSTALL.md#verifying-what-you-downloaded) for the commands.
