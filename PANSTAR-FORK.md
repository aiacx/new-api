# Panstar private relay fork

Production remains based on New API `v1.0.0-rc.37` at
`385d2dfd10d821b25c8a6766bd16eea248cb1652` plus the Panstar
commit `630a9c59ab2642c1609466f591bcbdb6a5e5b018`. The isolated upgrade
candidate rebases that reviewed commit onto official `v1.0.0-rc.38` at
`2906e4f779b715f282ae11203211dca77051d5af`; rc.38 is not recommended
for production by its maintainers.

Panstar-specific changes are deliberately limited to the private relay role:

- digest-authenticated service identity without a plaintext API token row;
- external customer-billing bypass while retaining zero-charge usage evidence;
- no request/response body in the private relay's debug logs;
- verified request ID and actual selected channel ID on the private response;
- no retry or automatic channel switch for Panstar service requests;
- non-root, revision-labelled and version-readable container builds.

Only a clean commit descending from the pinned rc.38 tag can be built by
`scripts/build-panstar-rc38.sh`. The script produces a linux/amd64 image,
archive, SPDX SBOM and checksummed evidence with the actual image ID; never
deploy `latest` or stock upstream in place of external billing. The image
digest is recorded after the build, not embedded in the image. The public
`/api/status` reports non-secret source/upstream identifiers; the image ID,
SBOM checksum, application config checksum, database migration evidence and
two-node readback are required separately for a GO.

Before migration, restore a schema/config backup into an isolated MySQL
instance and run the candidate migrator there. GORM AutoMigrate and the rc.38
constraint/index changes are not guaranteed reversible: a post-migration
rollback needs an explicitly verified compatibility path, forward fix, or
database backup restoration. The production runtime remains `NODE_TYPE=slave`
and must not auto-migrate; a one-time migration identity owns the change.

PIPIO must use a separate disabled Advanced Custom canary channel with
per-path passthrough, an isolated group, no converter, and no automatic
cross-channel retry. Keep existing channels untouched until the full Gateway
raw-data-plane and accounting contract is proven. Never configure Sub2API,
enable top-up, or claim WebSocket/hosted-tools capability from this candidate.

The service must remain private. Panstar user credentials terminate at the
Panstar Gateway and must never be sent to this process. The fork remains
subject to the repository's AGPLv3 license, Section 7 terms, NOTICE, and
third-party license files. Production use requires the approved Panstar
license/compliance decision and must not remove required attribution.
