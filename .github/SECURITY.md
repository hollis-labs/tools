# Security policy

## Supported versions

These tools are pre-1.0. Security fixes are made on `main` and released as a new patch version of the affected module. There are no backports.

## Report a vulnerability

Do not include an exploit, token, credential, database or other sensitive material in a public issue.

Use GitHub's private vulnerability-reporting flow when the repository's Security tab offers it. If it is unavailable, contact a repository maintainer privately through a contact channel published on the Hollis Labs organization or maintainer profile. Include the affected tool and version, reproduction steps, the security impact and a safe way to reach you. Maintainers will acknowledge a private report, investigate it and coordinate disclosure; response times are best effort.

`scripts/scan-public` runs in CI and scans the tree and history for secrets, internal hosts and personal paths.
