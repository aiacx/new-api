#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { echo 'usage: build-panstar-rc38.sh <artifact-directory>' >&2; exit 2; }
artifact_dir="$1"
repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_dir"

upstream_tag=v1.0.0-rc.38
upstream_commit=2906e4f779b715f282ae11203211dca77051d5af
[[ "$(git rev-parse "${upstream_tag}^{commit}")" == "$upstream_commit" ]] \
  || { echo 'official rc.38 tag does not resolve to the reviewed SHA' >&2; exit 1; }
[[ -z "$(git status --porcelain)" ]] \
  || { echo 'candidate source must be clean before building' >&2; exit 1; }
source_commit="$(git rev-parse HEAD)"
git merge-base --is-ancestor "$upstream_commit" "$source_commit" \
  || { echo 'candidate does not descend from reviewed rc.38' >&2; exit 1; }
[[ "$source_commit" != "$upstream_commit" ]] \
  || { echo 'stock upstream image cannot replace the external-billing fork' >&2; exit 1; }

image="panstar-new-api:rc38-${source_commit:0:12}"
umask 077
mkdir -p "$artifact_dir"
docker buildx build --platform linux/amd64 --load \
  --build-arg "BUILD_REVISION=$source_commit" \
  --build-arg "UPSTREAM_TAG=$upstream_tag" \
  --build-arg "UPSTREAM_COMMIT=$upstream_commit" \
  --build-arg BUILD_DIRTY=false \
  --tag "$image" .

[[ "$(docker image inspect -f '{{.Os}}/{{.Architecture}}' "$image")" == linux/amd64 ]] \
  || { echo 'candidate image is not linux/amd64' >&2; exit 1; }
[[ "$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.revision"}}' "$image")" == "$source_commit" ]] \
  || { echo 'candidate image revision label mismatch' >&2; exit 1; }
version_readback="$(docker run --rm --platform linux/amd64 --network none "$image" -version)"
[[ "$version_readback" == "$upstream_tag" ]] \
  || { echo 'candidate runtime version readback mismatch' >&2; exit 1; }

docker save "$image" | gzip -1 > "$artifact_dir/${image##*:}.tar.gz"
docker scout sbom --format spdx --output "$artifact_dir/${image##*:}.spdx.json" "local://$image"
image_id="$(docker image inspect -f '{{.Id}}' "$image")"
archive_sha="$(shasum -a 256 "$artifact_dir/${image##*:}.tar.gz" | cut -d ' ' -f 1)"
sbom_sha="$(shasum -a 256 "$artifact_dir/${image##*:}.spdx.json" | cut -d ' ' -f 1)"
jq -n --arg tag "$upstream_tag" --arg upstream "$upstream_commit" \
  --arg source "$source_commit" --arg image "$image" --arg imageId "$image_id" \
  --arg archiveSha "$archive_sha" --arg sbomSha "$sbom_sha" \
  '{upstreamTag:$tag, upstreamCommit:$upstream, panstarCommit:$source,
    dirty:false, platform:"linux/amd64", versionReadback:$tag, imageTag:$image, imageId:$imageId,
    imageArchiveSha256:$archiveSha, sbomSha256:$sbomSha}' \
  > "$artifact_dir/build-evidence.json"
echo "PANSTAR_NEWAPI_BUILD=PASS image=$image id=$image_id"
