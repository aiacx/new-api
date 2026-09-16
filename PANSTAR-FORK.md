# Panstar private relay fork

This fork is based on New API `v1.0.0-rc.37` at
`385d2dfd10d821b25c8a6766bd16eea248cb1652`.

Panstar-specific changes are deliberately limited to the private relay role:

- digest-authenticated service identity without a plaintext API token row;
- external customer-billing bypass while retaining zero-charge usage evidence;
- complete Codex standalone compaction field forwarding;
- removal of request-body debug logging;
- non-root, revision-labelled container builds.

The service must remain private. Panstar user credentials terminate at the
Panstar Gateway and must never be sent to this process. The fork remains
subject to the repository's AGPLv3 license, Section 7 terms, NOTICE, and
third-party license files. Production use requires the approved Panstar
license/compliance decision and must not remove required attribution.
