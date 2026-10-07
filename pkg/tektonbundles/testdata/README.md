# Test fixtures

Every file in `manifests/` is the raw, unmodified manifest bytes returned by
`quay.io` for the reference listed below, fetched anonymously by digest on
2026-10-06 (HYPERFLEET-1745 planning). `sha256(file)` equals the digest in the
file name and in the reference; the tests re-check this, so a fixture cannot be
silently edited or replaced.

File name: `<catalog task>_sha256_<hex>.json`, i.e. the catalog repository
`quay.io/konflux-ci/tekton-catalog/<catalog task>` at that digest.

## Source references

- `quay.io/konflux-ci/tekton-catalog/task-apply-tags@sha256:c177fc2b2482b1299e713c51bf258eba4972c016ef260d9f45335c2550dc7b2b`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:1cfcc0de161da38b5dd4b3b806a291688bcb50ffd41285f2ef72a772ce238896`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:4046f35c6d88ae8aa1b34063529382392704c848d4826f263535f92ac8f42ec0`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:7615bae91322179953527aedd1e855c31f5aa1b36592962db8a063f909635a96`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:88492a27a47e13308aac3f58014a20a1d4d3a85ab596eca6f0e383e25f6168a7`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:a4db7fcdbfff9db4e23902c00991089788db951778ead313f2352631b90be1ce`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:be1234f453a7236fb40c0d8b4d98313f3cfb7e967e1b4816c1881b8fc48ab915`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:cc59ae16baa756d23f3e5f8328cd9f8ba39a388f8eafbb9db666dea694814d25`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:e18fe17384dbb24f55a2538263346865211ac3016df8c1ca94a0fba80f7930e2`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:e54c00019c9d6c534c11db93ce0c8eecca3989375b0eef5517727e96e28af190`
- `quay.io/konflux-ci/tekton-catalog/task-build-helm-chart-oci-ta@sha256:eb6cab2cae1ce96567d1f21dbbdbe4453de1429ac5ab9cf48aca1fbd090eb5fd`
- `quay.io/konflux-ci/tekton-catalog/task-build-image-index@sha256:654446d8ec028b20ed6ada8a96486ff16692a25192755aa04a99cea8e0afb7e2`
- `quay.io/konflux-ci/tekton-catalog/task-buildah-oci-ta@sha256:3c57ec441d23c81d73df3b1f95cd5e800d53674fa0e8af784d81402e087e30bf`
- `quay.io/konflux-ci/tekton-catalog/task-clamav-scan@sha256:22fb08a2d08000aadf22a9cbaaa8265ba70e25fe4e24d81a3f3e64440d9e6b5e`
- `quay.io/konflux-ci/tekton-catalog/task-deprecated-image-check@sha256:0ccc688a77e9b7b0b8973c132a1e840844137e77f887be4a0bec8893b0776872`
- `quay.io/konflux-ci/tekton-catalog/task-ecosystem-cert-preflight-checks@sha256:439014132f4521ea3221ea29b1fd83e48f9728c071db6edaf6b870550e5d89a6`
- `quay.io/konflux-ci/tekton-catalog/task-git-clone-oci-ta@sha256:a3678910c2bf9187a66a96066193cb41046fbb7097160bf2be6a374ed5e49d30`
- `quay.io/konflux-ci/tekton-catalog/task-init@sha256:7854d7babe4136e53e337f6433d952532314433b48bb62b422ddc02700934d85`
- `quay.io/konflux-ci/tekton-catalog/task-prefetch-dependencies-oci-ta@sha256:0c386f2c26b46b978280c5c54caac26bcd524975f8f7cc06bb080ba00eae3ad4`
- `quay.io/konflux-ci/tekton-catalog/task-push-dockerfile-oci-ta@sha256:4c567d11296850230fe32e76d129834d3bf337a0cae9989b64279b7cfaa4e735`
- `quay.io/konflux-ci/tekton-catalog/task-roxctl-scan@sha256:8286d4dd5a337596ed4776d68152656abed9a464777dbb87b00256fd5987789e`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:4b1856e41d087e53af26562fb16a490579777d9ff05af33011ce212d477c7cd5`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:950ff25761458ba3a2b44748778c1cb31656149a4a3d56be4b32434d37827270`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:12239e624c5bca759fc594bdefd3d4411a62c69bb4c29c5ad733b1e0d88fb8e9`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:cc1b939d933d24727ef4b90e1c5c65a6d2a6c1c036cf74f685b597d611fa150f`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:e2875a6c1368c3ed78dca9d01a6869ea6fdef03449e98970c8ba313095b58eff`
- `quay.io/konflux-ci/tekton-catalog/task-rpms-signature-scan@sha256:e9088ff4bcc315abe05076cd066a27db058ced1e6fa177abda6840f16aff6e8b`
- `quay.io/konflux-ci/tekton-catalog/task-run-opm-command-oci-ta@sha256:155d18958065b9f6fd7a0d333a6d098db78145d4fa9ddb8f18abcd0e4eb73bc8`
- `quay.io/konflux-ci/tekton-catalog/task-sast-shell-check-oci-ta@sha256:d00c884c78489a52dc638e6ced0a01429670d0754f5b1305ad576709cf660312`
- `quay.io/konflux-ci/tekton-catalog/task-sast-snyk-check-oci-ta@sha256:17b9587eba53fabaab35061275306bfe1f99661d55e14e22f62321fa7cb5ff3e`
- `quay.io/konflux-ci/tekton-catalog/task-sast-unicode-check-oci-ta@sha256:15d654c9576b9c4ee1f78b09fce541acf9afe33f1c2363fd6219a79dbf62c95d`
- `quay.io/konflux-ci/tekton-catalog/task-slack-webhook-notification@sha256:55fdfbe3b4a565aa58fc76d6209a3b69832f43373ee6e33555bb69423a8b9ceb`
- `quay.io/konflux-ci/tekton-catalog/task-source-build-oci-ta@sha256:45091b5022754d452b4de60c7d7f4c6e03626efff55f96e9af937400fe3506eb`

## Classes

- SARIF artifacts (`artifactType: application/sarif+json`, a single layer with no
  `dev.tekton.image.*` annotations) that were briefly or currently published under
  the `0.2.2` / `0.4.0` bundle tags: `task-rpms-signature-scan` `12239e62...`,
  `e9088ff4...`; `task-build-helm-chart-oci-ta` `1cfcc0de...`, `88492a27...`,
  `a4db7fcd...`, `4046f35c...`. The Tekton bundle resolver rejects these.
- Valid bundles: everything else, including `be1234f4...` (valid, though its runtime
  lacked `yq`) and `task-run-opm-command-oci-ta@sha256:155d1895...` (a test
  pipeline in layer 0, the task in layer 1).

## Deleted digests (no fixture)

These digests were deleted upstream and now answer `MANIFEST_UNKNOWN`, so there
are no bytes to keep. The tests model them with an `httptest` registry that
returns 404 `MANIFEST_UNKNOWN`:

- `task-rpms-signature-scan@sha256:0f97bf8b6537ce98009ea8d336f02d48e7a8be1fb2ec5b42be3969e6d6788f76`
  (the subject of the `12239e62...` SARIF artifact)
- `task-build-helm-chart-oci-ta@sha256:1dfb6577...` and `@sha256:2712ff1a...`
  (only the digest prefixes were recorded)

The synthetic parity cases (annotation, kind, layer-count, index and malformed
JSON cases) are built in Go by the tests, not stored here.
