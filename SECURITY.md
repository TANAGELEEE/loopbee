# Security

Loopbee runs agent CLIs on your computer, keeps credentials in the system keychain and updates itself from this
repository's GitHub releases. Please report vulnerabilities privately, not in a public issue.

## Reporting

Use **Security › Report a vulnerability** on this repository (GitHub private vulnerability reporting). Include the
version (`beectl version` or Settings › General), the platform and the steps to reproduce.

## Supported versions

Only the latest release receives fixes. Released builds update themselves; a build from source does not.

## In scope

- Escaping a Loop's permission level or sandbox through Loopbee (see [contracts](docs/contracts.md)).
- The loopback API: authentication, Origin/Host checks, DNS rebinding.
- Updates: anything that installs a file that does not match the release's `SHA256SUMS`
  (see [updates](docs/subsystems/updates.md)).
- Issue intake passing text from people without write access to an agent.
- Credentials leaving the keychain, reaching a commit, an export or a notification.

What an agent CLI itself does inside the level you gave it is reported to that CLI's project.
