# [1.20.0](https://github.com/posit-dev/ptd/compare/v1.19.0...v1.20.0) (2026-04-16)


### Bug Fixes

* make snapshot dynamic, handle numeric IDs, add edge case tests ([84768b8](https://github.com/posit-dev/ptd/commit/84768b8fb439b107558283284a5899d4c80267ad))


### Features

* add config strip and snapshot for eject severance ([9d931b0](https://github.com/posit-dev/ptd/commit/9d931b0af3e696d4bc9fca2a0a74dda11103246d))

# [1.19.0](https://github.com/posit-dev/ptd/compare/v1.18.0...v1.19.0) (2026-04-16)


### Features

* conditionally include control room remote_write in Alloy config ([9a141df](https://github.com/posit-dev/ptd/commit/9a141dfafa66525839cd578f8d67ae3ff80fc2ef))

# [1.18.0](https://github.com/posit-dev/ptd/compare/v1.17.0...v1.18.0) (2026-04-16)


### Features

* add IAM trust removal runbook for eject bundle ([09fd47e](https://github.com/posit-dev/ptd/commit/09fd47ecb2d62c0ec2772563dfa1b99849f7f66c))
* add re-adopt runbook generator for eject bundle ([9986433](https://github.com/posit-dev/ptd/commit/99864332054da0185dfa0ebc3922078ddea29280))
* add RemoveWorkloadMimirPassword for eject severance ([5cafed5](https://github.com/posit-dev/ptd/commit/5cafed59643084cebb315f820696e3a2832e0242))
* cloud-specific access removal runbooks, wire into eject ([e544235](https://github.com/posit-dev/ptd/commit/e544235431e3bacc43a2aaf78ee84dc6ad794d05))
* tolerate nil control room target in ensure steps ([bc3e99a](https://github.com/posit-dev/ptd/commit/bc3e99af1d0b07acacd953f964bac65fa3bb104d))

# [1.17.0](https://github.com/posit-dev/ptd/compare/v1.16.0...v1.17.0) (2026-04-15)


### Bug Fixes

* **clusters:** address PR review — feature gaps and deduplication ([619227e](https://github.com/posit-dev/ptd/commit/619227ec573e883864cf23aaea0628518c628800))


### Features

* rewrite clusters step in Go, retire Python implementation ([1da055e](https://github.com/posit-dev/ptd/commit/1da055e22d17fb592aaf6ba2369ae33949b9b52d))

# [1.16.0](https://github.com/posit-dev/ptd/compare/v1.15.0...v1.16.0) (2026-04-15)


### Bug Fixes

* **justfile:** make codesign conditional on macOS to fix Linux CI ([3520692](https://github.com/posit-dev/ptd/commit/352069291c63034d0c5c05be7131e130fa8bd376))
* **sites:** use exec plugin kubeconfig to eliminate token-rotation state diffs ([6a815e3](https://github.com/posit-dev/ptd/commit/6a815e3124ae47154cc4a4c103eec2cc67f8eb21))


### Features

* add Claude auto-review and PR assistant workflows ([ac2d4b7](https://github.com/posit-dev/ptd/commit/ac2d4b7cb6151baca1c3ac104507608403a49f49))

# [1.15.0](https://github.com/posit-dev/ptd/compare/v1.14.0...v1.15.0) (2026-04-14)


### Bug Fixes

* drop mimir from workload purpose string, hoist siteSecretFields ([c6a4913](https://github.com/posit-dev/ptd/commit/c6a491379a770c28cd57fb8bf132221ea95ba67a))
* remove okta-oidc-client-creds from secret catalog ([7ad61c1](https://github.com/posit-dev/ptd/commit/7ad61c179cd4047b5d2e903fe06e24150bab32b1))


### Features

* enumerate secret references from known PTD conventions ([f75ab6c](https://github.com/posit-dev/ptd/commit/f75ab6c2c34cef3ca57b73cfe2b3190f87db916e)), closes [#215](https://github.com/posit-dev/ptd/issues/215)

# [1.14.0](https://github.com/posit-dev/ptd/compare/v1.13.0...v1.14.0) (2026-04-13)


### Features

* remove automated kustomize-to-Helm migration job from TeamOperator ([e0233b5](https://github.com/posit-dev/ptd/commit/e0233b5502a063a9b972a7b9f5573fc07690a923))

# [1.13.0](https://github.com/posit-dev/ptd/compare/v1.12.0...v1.13.0) (2026-04-13)


### Bug Fixes

* check out.Close() error in copyFile to catch flush failures ([ebd45c6](https://github.com/posit-dev/ptd/commit/ebd45c6c115738894c938eefe911a6b53d12d59e))
* sanitize file paths in config copy to resolve Snyk findings ([65d075f](https://github.com/posit-dev/ptd/commit/65d075f7a8eeee8da96c3073a25d76813b81db2c))


### Features

* copy workload config files to eject bundle ([7b69dc6](https://github.com/posit-dev/ptd/commit/7b69dc6c2397e7418a0199d288fa362f28ffa07d)), closes [#214](https://github.com/posit-dev/ptd/issues/214)

# [1.12.0](https://github.com/posit-dev/ptd/compare/v1.11.0...v1.12.0) (2026-04-13)


### Features

* enable Pulumi debug logging when -v flag is set ([f165a01](https://github.com/posit-dev/ptd/commit/f165a01878e75b6bc79dd4eeebec17e7b847d468))

# [1.11.0](https://github.com/posit-dev/ptd/compare/v1.10.0...v1.11.0) (2026-04-08)


### Features

* extract resource physical IDs from Pulumi state ([0bb4e32](https://github.com/posit-dev/ptd/commit/0bb4e32cbb96821bcb18597aad7843592139eb80)), closes [#212](https://github.com/posit-dev/ptd/issues/212)

# [1.10.0](https://github.com/posit-dev/ptd/compare/v1.9.0...v1.10.0) (2026-04-08)


### Features

* collect control room connection details for eject ([74f8d30](https://github.com/posit-dev/ptd/commit/74f8d30906d38f4e7074d969ae38bd687ee5aae9)), closes [#211](https://github.com/posit-dev/ptd/issues/211)

# [1.9.0](https://github.com/posit-dev/ptd/compare/v1.8.5...v1.9.0) (2026-04-08)


### Features

* scaffold ptd eject command ([b4584ae](https://github.com/posit-dev/ptd/commit/b4584ae011d8e2bdea05bdc48b7ad34b76814751)), closes [#210](https://github.com/posit-dev/ptd/issues/210)

## [1.8.5](https://github.com/posit-dev/ptd/compare/v1.8.4...v1.8.5) (2026-04-08)


### Bug Fixes

* add mutex to postgres config test mock to prevent data race ([4e32dc3](https://github.com/posit-dev/ptd/commit/4e32dc3e9a38d3c7b4de7a7422eb24b3be086392))

## [1.8.4](https://github.com/posit-dev/ptd/compare/v1.8.3...v1.8.4) (2026-04-08)


### Bug Fixes

* pin bastion AMI regex to kernel-6.18 variant ([57a7902](https://github.com/posit-dev/ptd/commit/57a79026aa240dfbcec55828c52dfa80facfce58))

## [1.8.3](https://github.com/posit-dev/ptd/compare/v1.8.2...v1.8.3) (2026-04-08)


### Bug Fixes

* use runtime.Caller instead of git rev-parse in test setup ([979ef52](https://github.com/posit-dev/ptd/commit/979ef52d5dd0dd5aeeba7699c59580c83a5c549a))

## [1.8.2](https://github.com/posit-dev/ptd/compare/v1.8.1...v1.8.2) (2026-04-07)


### Bug Fixes

* update uv.lock for pulumi-aws 6.78.0 ([805771a](https://github.com/posit-dev/ptd/commit/805771a4301ca2468fef2a539cdf4dc0bdc64e89))
* upgrade pulumi-aws and pass force_update_version to both Cluster and NodeGroup ([128f232](https://github.com/posit-dev/ptd/commit/128f232f7f236e5346ff90a85d59f72e32fb6097))

## [1.8.1](https://github.com/posit-dev/ptd/compare/v1.8.0...v1.8.1) (2026-04-07)


### Bug Fixes

* use runtime.Caller instead of git rev-parse in test setup ([69edc7a](https://github.com/posit-dev/ptd/commit/69edc7abbd19f46b6fc996352d810e1a8e4d6915))

# [1.8.0](https://github.com/posit-dev/ptd/compare/v1.7.1...v1.8.0) (2026-04-06)


### Bug Fixes

* adopt existing FelixConfiguration before Helm manages it ([063b26e](https://github.com/posit-dev/ptd/commit/063b26e1fb7c6630c50da6f2a55b9f4ba737c6ab))
* dataclass inheritance ordering and ruff FBT lint errors ([a496abe](https://github.com/posit-dev/ptd/commit/a496abe4a6eb2fc0ec04979bb0103fd1b1fcb4b3))
* drop Nftables dataplane and CRD patch, stay on Iptables ([70b5d6c](https://github.com/posit-dev/ptd/commit/70b5d6c08948c4b31f4ab1154b7e266e219cfb8b))
* force NFT iptables backend for Calico on AL2023 ([8da6d66](https://github.com/posit-dev/ptd/commit/8da6d66a8e7677003d45ff0a2c193be9e744fda4))
* patch Installation CR to enforce Calico CNI on EKS ([5fc673e](https://github.com/posit-dev/ptd/commit/5fc673e455f414f5dab8f15d4b7a73320c04ee11))
* remove unnecessary FelixConfiguration adoption patch ([42cef46](https://github.com/posit-dev/ptd/commit/42cef46c82d23dbea6c17b2ba44dd9c3fa7b89cf))
* restore FelixConfiguration adoption patch with ignore_changes to prevent drift ([c56f7eb](https://github.com/posit-dev/ptd/commit/c56f7ebdc9a832286e1d48a6f94dc9927bfc52bb))


### Features

* add third_party_telemetry_enabled config to disable infra telemetry ([1135cbe](https://github.com/posit-dev/ptd/commit/1135cbe885e26fe011bf0abe77e7c317b5243dd8))
* pre-patch Installation CRD to allow Nftables dataplane on upgrade ([cdfcfb1](https://github.com/posit-dev/ptd/commit/cdfcfb1e4b17973f71016c6ab38ea6ad34157da7))
* upgrade Tigera Operator 3.29.3 → 3.31.4 ([cea1639](https://github.com/posit-dev/ptd/commit/cea16396e6ecbf6f1dd59487c5472819844696e8))

## [1.7.1](https://github.com/posit-dev/ptd/compare/v1.7.0...v1.7.1) (2026-04-06)


### Bug Fixes

* disable azure load balancer alerts until fixed ([2f2fe6f](https://github.com/posit-dev/ptd/commit/2f2fe6fd39d099ac390bab8ed3ef347af2a758c0))
* update tests to reflect disabled loadbalancer alerts ([16f7a8b](https://github.com/posit-dev/ptd/commit/16f7a8b44e8451e9d1a03d97ba417531c4c88686))

# [1.7.0](https://github.com/posit-dev/ptd/compare/v1.6.0...v1.7.0) (2026-04-02)


### Features

* add var to enable shell identification while using workon ([f9bf9ef](https://github.com/posit-dev/ptd/commit/f9bf9ef58933d9a63514228472c1f0d75052790d))

# [1.6.0](https://github.com/posit-dev/ptd/compare/v1.5.2...v1.6.0) (2026-04-02)


### Features

* new netapp throughput limit alert ([5678437](https://github.com/posit-dev/ptd/commit/56784378d0d1033e1a5929f327ea56502e9c6613))

## [1.5.2](https://github.com/posit-dev/ptd/compare/v1.5.1...v1.5.2) (2026-03-31)


### Bug Fixes

* bump default alb latency alert threshold ([fec5940](https://github.com/posit-dev/ptd/commit/fec59406a65b46ebed37b98db7089c1c3576aa68))

## [1.5.1](https://github.com/posit-dev/ptd/compare/v1.5.0...v1.5.1) (2026-03-26)


### Bug Fixes

* bump go directive to 1.25.6 (CVE-2025-61728, CVE-2025-61726) ([d416b1a](https://github.com/posit-dev/ptd/commit/d416b1a70ee736f04d3b8af4c6599f1dd79b55a8))
* upgrade google.golang.org/grpc to v1.79.3 (CVE-2026-33186) ([d882605](https://github.com/posit-dev/ptd/commit/d8826058e3a7ba7eee8b9924b6d1ebd912d9ffa9))

# [1.5.0](https://github.com/posit-dev/ptd/compare/v1.4.2...v1.5.0) (2026-03-20)


### Bug Fixes

* add tenant label back to metrics alert ([53dfbc2](https://github.com/posit-dev/ptd/commit/53dfbc23a5b2ce7e0ae3144bac3214ac173cff7c))
* alloy instance duplication bug ([ab2e645](https://github.com/posit-dev/ptd/commit/ab2e645a2c48b229c5027659faed5b89284ed0f8))
* azure load balancer metrics resource group and azure alert queries and formatting ([767acff](https://github.com/posit-dev/ptd/commit/767acffaef55e81750119a85403d3417929b84c0))
* bump default aws alb idle connection timeout ([3440fdf](https://github.com/posit-dev/ptd/commit/3440fdf61bfe2173dd0a4fdfe13e06d39101de75))
* change azure metric names and give better alert descriptions ([16fc658](https://github.com/posit-dev/ptd/commit/16fc6586b0c348aa3e5f0e59b6ab8c7d2b3afa08))
* correct workload.go ([089dce8](https://github.com/posit-dev/ptd/commit/089dce822ab9b475c9fd744aa43f8c2d58318774))
* correct worktree path in CLAUDE.md ([c28115e](https://github.com/posit-dev/ptd/commit/c28115e63a283b31be828e35aa5783c47cf06004))
* **docs:** correct dashboard deployment documentation inaccuracies ([ab73129](https://github.com/posit-dev/ptd/commit/ab7312978d31232e961816b9b0fc916421375f62))
* **docs:** correct dashboard UID description and add trailing newline ([2ebb899](https://github.com/posit-dev/ptd/commit/2ebb8992c80bdae5985799441d4aabf964ebef1e))
* ensure all alerts are always created ([28ccbf2](https://github.com/posit-dev/ptd/commit/28ccbf23a0e6351ee785568659cb8e9a1c3addb7))
* **grafana:** add missing cluster filters to unlinked panels in Posit Team Overview ([05fdccc](https://github.com/posit-dev/ptd/commit/05fdccc7d0da06d5b78a88defb1f5989ebc7e880))
* **grafana:** apply site filter consistently and correct version in Posit Team Overview ([d477d44](https://github.com/posit-dev/ptd/commit/d477d44bdbfb87a2aa7984234c93e8706e1de9d4))
* **grafana:** correct Connect panel titles to match query semantics ([9396999](https://github.com/posit-dev/ptd/commit/93969998026756c858d17024b4355b396638b08e))
* **grafana:** correct dashboard provisioning settings for posit_team_overview ([1284bdc](https://github.com/posit-dev/ptd/commit/1284bdc98f052b09ed9a51e4f6d5dd2b98cf5480))
* **grafana:** enable multi-cluster support for Kubernetes Global View dashboard ([388e320](https://github.com/posit-dev/ptd/commit/388e320e8090e6f9a4fd0eb730aeef3de54e648e))
* **grafana:** fix Package Manager panel query and display issues ([b0d8b1f](https://github.com/posit-dev/ptd/commit/b0d8b1f7a26e57d2c9e352a47ef4e94ea8841321))
* **grafana:** handle division by zero and fix labels in License Consumption gauge ([0337f5a](https://github.com/posit-dev/ptd/commit/0337f5aea0195aec734efa78285122fea3ab234e))
* **grafana:** prevent automatic time unit conversion in License days left panel ([fcc0ecb](https://github.com/posit-dev/ptd/commit/fcc0ecb0d336b728b82e8e97f47fbe5afc845978))
* **grafana:** remove inaccurate License expires panels from dashboard ([dbc436f](https://github.com/posit-dev/ptd/commit/dbc436f506ce8d0a08fd59e341c2607eb757cae4))
* **grafana:** remove orphaned cluster references from posit-team-overview transformations ([1c588e0](https://github.com/posit-dev/ptd/commit/1c588e02bb54c4c324ba35110f42895564318b39))
* **grafana:** standardize label ordering in by() clauses for license metrics ([63e1166](https://github.com/posit-dev/ptd/commit/63e1166676e1a32d6d5c95eb3a26c9d0f48da01a))
* **grafana:** use max aggregation for Connect global metrics ([0263879](https://github.com/posit-dev/ptd/commit/02638796f8c613e9ed446c998e7cf3989e50203a))
* **grafana:** use pattern match operator for site filter in Posit Team Overview ([de943c0](https://github.com/posit-dev/ptd/commit/de943c0dddcbf845631787d4ddd382021f86782e))
* improve alerting when no metrics received from one or all workloads ([9ffa0e8](https://github.com/posit-dev/ptd/commit/9ffa0e8398c9b4975dd3c28254f1dc8ee49401ef))
* lint ([ae55544](https://github.com/posit-dev/ptd/commit/ae555443b5b0c895deb7ff7543a310cb45320263))
* **python-pulumi:** implement robust RFC 1123 name sanitization with validation ([91e56c0](https://github.com/posit-dev/ptd/commit/91e56c03e8a612de3937e70731c73f67f5877e06))
* **python-pulumi:** resolve linter warnings in dashboard code ([cef03bb](https://github.com/posit-dev/ptd/commit/cef03bb400625c79d8f21934eec04435f439d10f))
* **python-pulumi:** sanitize dashboard names for Kubernetes RFC 1123 compliance ([1008253](https://github.com/posit-dev/ptd/commit/10082536a292d5dfedd040a734e9410d2bb124d3))
* quote descriptions in alerts with colon characters ([2423c6a](https://github.com/posit-dev/ptd/commit/2423c6aacee14025ec4867e0316ba83a263730b4))
* remove client_id and secrets_provider_client_id from azure_workload fixture ([f6a3fb9](https://github.com/posit-dev/ptd/commit/f6a3fb912fb387393a94c6f5dc60fa9400c3bb65))
* remove workload alert sidecar and fix azure resource graph query syntax ([6f83f76](https://github.com/posit-dev/ptd/commit/6f83f76f41fc0e84c8cc269d40aa1b436e493036))
* replace underscores in alerts generated via file ([ebf2319](https://github.com/posit-dev/ptd/commit/ebf23196a171b5f2eb81401b8aab0d2e42d5e5e6))
* resolve lint errors in test fixtures and conftest ([c69be20](https://github.com/posit-dev/ptd/commit/c69be2011bb8596fe03efdd5ff9c8d896ef9cac4))
* solve intermittent no data result with netapp latency alerts and adjust thresholds based on current workloads ([65fad8f](https://github.com/posit-dev/ptd/commit/65fad8f444a30cbdadee96c444dd0997a19f44bc))
* stop overriding team-operator image when not explicitly configured ([49f7b2c](https://github.com/posit-dev/ptd/commit/49f7b2c510fa5ab6cae59ef99f8c616fd2cc08c6))
* undo unrelated change ([569c8b3](https://github.com/posit-dev/ptd/commit/569c8b354f29fc5183cdb45ebb6dc5d569eacfff))
* undo unrelated change ([a7af92d](https://github.com/posit-dev/ptd/commit/a7af92d183319c8c8e7c68a1072956f27e12b5af))
* use custom_role for EKS access entry when configured ([d1a4aee](https://github.com/posit-dev/ptd/commit/d1a4aee6f62ca8ca66b330f9df771a7c6ced4d33))


### Features

* add Go↔Python config sync validation and standardize test fixtures ([bfa9f3d](https://github.com/posit-dev/ptd/commit/bfa9f3d343ec65d4ca7c1d4f1bcae4e862721308))
* add ppm-oidc-client-secret to site secret provisioning ([46ae5ac](https://github.com/posit-dev/ptd/commit/46ae5ace3457b775de80104f6cc79e2dc8afac7c))
* allow force for cluster upgrades ([fb990a3](https://github.com/posit-dev/ptd/commit/fb990a3ba4b9d0bc9f891f758e1ef4b3b41d332e))
* automatically recreate azure bastion vm with latest version ([0384239](https://github.com/posit-dev/ptd/commit/0384239007e4756f5792010083682cd162b89ee0))
* **azure:** add configurable bastion instance type ([36bb44d](https://github.com/posit-dev/ptd/commit/36bb44d557254479dd0d6eb3f0557a1ce379fafc))
* **grafana:** add cluster filter to all Posit Team Overview dashboard panels ([dcba6f2](https://github.com/posit-dev/ptd/commit/dcba6f22939f9621385009b061ebb85ec05de486))
* **grafana:** add Connect row to Posit Team Overview dashboard ([1440b2c](https://github.com/posit-dev/ptd/commit/1440b2ccfcc472c935e997156c770ea9d9e83a5b))
* **grafana:** add Package Manager row to Posit Team Overview dashboard ([03ea325](https://github.com/posit-dev/ptd/commit/03ea325430ba78e3d4acf59b61864cf60f5df5a8))
* **grafana:** improve Running Version panel display in Posit Team Overview dashboard ([cdd1b7a](https://github.com/posit-dev/ptd/commit/cdd1b7a62f8252d45933165b09b6378b7b417ca0))
* support per-workload custom tags on AKS resources ([e52c0c0](https://github.com/posit-dev/ptd/commit/e52c0c0ee07f79f44683788e127f99e51311eb9f))
* support setting externally created route table on private azure subnet ([a0f3711](https://github.com/posit-dev/ptd/commit/a0f3711f7cbb21077c811fe025f5ce3d2a7c7641))


### Reverts

* undo unintended Justfile change ([4858593](https://github.com/posit-dev/ptd/commit/4858593e4f435e728a06e55bc34bcdb98bb4183e))

## [1.4.2](https://github.com/posit-dev/ptd/compare/v1.4.1...v1.4.2) (2026-02-13)


### Bug Fixes

* do not use key auth for storage accounts due to security warnings ([3e4e860](https://github.com/posit-dev/ptd/commit/3e4e860889f9d52fcfe78d67f7d5de1eb56546c8))
* enable azure auth in the cli when run in AWS Workspace ([5b846e4](https://github.com/posit-dev/ptd/commit/5b846e4866827ed790874a30dbb05423407f3ee1))

## [1.4.1](https://github.com/posit-dev/ptd/compare/v1.4.0...v1.4.1) (2026-02-10)


### Bug Fixes

* **eks:** add explicit resource dependencies for cluster provisioning ([072f84e](https://github.com/posit-dev/ptd/commit/072f84ebdd870cf5cee7b3a3bc6b64602295eb28))
* **eks:** restore parallel execution for Tigera and node groups ([d6a8587](https://github.com/posit-dev/ptd/commit/d6a858703b16ed37a036dba891f1585a8cf0da69))
* **persistent:** remove AWS-only guard from mimir password sync ([9e70212](https://github.com/posit-dev/ptd/commit/9e702129903fb69ddc4ebdf98fb8de4d3b6481ac))
* **persistent:** skip mimir password check for control rooms ([7bec570](https://github.com/posit-dev/ptd/commit/7bec570f3e5145fc6484156ffa39b7c268b45804))
* **team-operator:** create posit-team-system namespace before migration resources ([b074d4f](https://github.com/posit-dev/ptd/commit/b074d4f1cd67c4ffbff7075bac4fbb71338e1100))
* **team-operator:** skip await on Helm release to debug failures ([35b38a3](https://github.com/posit-dev/ptd/commit/35b38a3867441d2704b3a259fcecd207bb408cec))
* **tigera:** update Calico Helm chart repository URL ([511bbb3](https://github.com/posit-dev/ptd/commit/511bbb3bc5217994316b4934be0619a46ab25394))


### Reverts

* remove skip_await from team-operator Helm release ([f2c8293](https://github.com/posit-dev/ptd/commit/f2c8293b1214607d71e830b9219936808c72bdd9))
* **team-operator:** remove explicit posit-team-system namespace ([7b64328](https://github.com/posit-dev/ptd/commit/7b6432843a103a734744f5f6f13310dfb4ed8247))

# [1.4.0](https://github.com/posit-dev/ptd/compare/v1.3.0...v1.4.0) (2026-02-09)


### Bug Fixes

* remove env copy ([931661d](https://github.com/posit-dev/ptd/commit/931661d22c34e31504c6e13d74c891d0ae6b1267))


### Features

* add azure workload support to k9s command ([44d135c](https://github.com/posit-dev/ptd/commit/44d135cea78879a29b525668abc5b4611b17786b))

# [1.3.0](https://github.com/posit-dev/ptd/compare/v1.2.1...v1.3.0) (2026-02-06)


### Bug Fixes

* **lib:** fix flaky TestGenerateRandomString test ([74755d3](https://github.com/posit-dev/ptd/commit/74755d33a3f0b6fe7062935b6b57a9d81244c5b5))


### Features

* **control-room:** add EKS access entries support ([b739db1](https://github.com/posit-dev/ptd/commit/b739db163c8fe5de2f5c9d61eeaf1b7727909d85)), closes [#79](https://github.com/posit-dev/ptd/issues/79)
* **eks:** enable access entries by default ([3a538f6](https://github.com/posit-dev/ptd/commit/3a538f6c10d7a8edb901504122b3fe5379d3471a)), closes [#111](https://github.com/posit-dev/ptd/issues/111)

## [1.2.1](https://github.com/posit-dev/ptd/compare/v1.2.0...v1.2.1) (2026-02-03)


### Bug Fixes

* support workon for custom steps ([2ef2752](https://github.com/posit-dev/ptd/commit/2ef2752392f851a19521e5b714a6db72da6e0226))

# [1.2.0](https://github.com/posit-dev/ptd/compare/v1.1.3...v1.2.0) (2026-02-03)


### Features

* add workflow to handle team-operator version updates ([2541d23](https://github.com/posit-dev/ptd/commit/2541d2336c9cf1838c01eb8323e35280cb17eb28))

## [1.1.3](https://github.com/posit-dev/ptd/compare/v1.1.2...v1.1.3) (2026-01-28)


### Bug Fixes

* **team-operator:** add retain_on_delete protection for CRDs and namespace ([8c2d8ce](https://github.com/posit-dev/ptd/commit/8c2d8ced41953afa6f1a0bd4e5c5db18f3b29880))
* **team-operator:** simplify to namespace protection only ([39c179a](https://github.com/posit-dev/ptd/commit/39c179a022b3b2078f25a8b316054fbc83a7c45b))

## [1.1.2](https://github.com/posit-dev/ptd/compare/v1.1.1...v1.1.2) (2026-01-28)


### Bug Fixes

* **fsx:** ignore daily_automatic_backup_start_time in diffs ([ecf7cb0](https://github.com/posit-dev/ptd/commit/ecf7cb0f2018d69d298a6be775dc306abb94d0db)), closes [#5](https://github.com/posit-dev/ptd/issues/5)

## [1.1.1](https://github.com/posit-dev/ptd/compare/v1.1.0...v1.1.1) (2026-01-27)


### Bug Fixes

* clean up repo references to use posit-dev ([470b829](https://github.com/posit-dev/ptd/commit/470b829129af20257a60cff878e4e9dcc9f6b11d))

# [1.1.0](https://github.com/posit-dev/ptd/compare/v1.0.2...v1.1.0) (2026-01-21)


### Features

* **monitoring:** add container metrics collection for pod debugging ([23b597f](https://github.com/posit-dev/ptd/commit/23b597f72a6a01c356cb2a7d64453901041a3f09))

## [1.0.2](https://github.com/posit-dev/ptd/compare/v1.0.1...v1.0.2) (2026-01-21)


### Bug Fixes

* add helm.sh/resource-policy: keep to CRD patch ([e175604](https://github.com/posit-dev/ptd/commit/e175604caacb464371f22cf37393b35d8ee9ed9c))

## [1.0.1](https://github.com/posit-dev/ptd/compare/v1.0.0...v1.0.1) (2026-01-16)


### Bug Fixes

* add missing site yaml for sites step ([e28e8e5](https://github.com/posit-dev/ptd/commit/e28e8e5a80ea788b5d33c26eae7f7e1c78e4368e))

# 1.0.0 (2026-01-15)


### Features

* add documentation (docs/) ([986bec5](https://github.com/posit-dev/ptd/commit/986bec56f2d4eb0e55b03125f124063fdd09c723))
* add end-to-end tests (e2e/) ([bc3a4b0](https://github.com/posit-dev/ptd/commit/bc3a4b0ba1924c0caf87d6e5b2bb9df699f549e2))
* add example configurations (examples/) ([18a4683](https://github.com/posit-dev/ptd/commit/18a4683be572e2119886d1131e72ec45e53e5f41))
* add GitHub Actions workflows (.github/workflows/) ([bd44f3b](https://github.com/posit-dev/ptd/commit/bd44f3bd3543e5d8a5895494b4dcfacb50d5e7d2))
* add Go CLI (cmd/) ([07fd413](https://github.com/posit-dev/ptd/commit/07fd4132afb21d5c97370a1a855186aec1ce19d6))
* add project configuration files ([be217cd](https://github.com/posit-dev/ptd/commit/be217cd714cc7a94de595a54c68c90ca4e85ec94))
* add Python Pulumi IaC package (python-pulumi/) ([84cbe96](https://github.com/posit-dev/ptd/commit/84cbe96283d41292783cc44d6bbe66adb0b2902c))
* add root build and config files ([50adb12](https://github.com/posit-dev/ptd/commit/50adb12af85c6cdf4938cff341953727c51a87be))
* add shared Go libraries (lib/) ([3d52c6f](https://github.com/posit-dev/ptd/commit/3d52c6f4763ab2694373c8dec7f72d3d04fdd7aa))
* **ci:** add semantic versioned releases ([12cbfba](https://github.com/posit-dev/ptd/commit/12cbfbaf7553e6453cc233b5f4df8acfe68332cb))
