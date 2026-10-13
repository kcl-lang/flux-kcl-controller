# flux-kcl-controller

A Helm chart for the [flux-kcl-controller](https://github.com/kcl-lang/flux-kcl-controller).

## Requirements

- Kubernetes >= 1.28
- Flux installed in the cluster, providing the `GitRepository`, `OCIRepository`,
  `Bucket` and `Kustomization` CRDs

## Installing

```shell
helm install flux-kcl-controller ./charts/flux-kcl-controller \
  --namespace flux-system --create-namespace
```

To manage the controller with Flux itself, apply
[`install/rc.yaml`](../../install/rc.yaml) instead.

## Values

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Number of controller replicas. Leader election must be enabled for more than one to be useful. |
| `image.repository` | `ghcr.io/kcl-lang/flux-kcl-controller` | Controller image repository. |
| `image.tag` | `""` | Image tag. Defaults to the chart `appVersion`. |
| `image.digest` | `""` | Image digest. Takes precedence over `image.tag`; set it for reproducible installs. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Image pull secrets. |
| `nameOverride` | `""` | Overrides the chart name used in resource names. |
| `fullnameOverride` | `""` | Overrides the fully qualified resource name. |
| `serviceAccount.create` | `true` | Create a ServiceAccount for the controller. |
| `serviceAccount.name` | `""` | Name of the ServiceAccount to run as. Defaults to the release fullname. |
| `serviceAccount.annotations` | `{}` | Annotations for the ServiceAccount. |
| `rbac.create` | `true` | Create the ClusterRole and ClusterRoleBinding. |
| `rbac.clusterAdminBinding.create` | `true` | Bind the controller service account to `cluster-admin`. See the warning below. |
| `rbac.extraRules` | `[]` | Additional rules appended to the controller ClusterRole. |
| `service.metrics.bindAddress` | `":8083"` | Address the metrics endpoint binds to. The container port and probes are derived from it; set to `0` or `none` to disable metrics. |
| `service.events.addr` | `""` | Address of the notification-controller webhook. Empty records events in the cluster only. |
| `watch.allNamespaces` | `true` | Watch KCLRun objects in every namespace. When false, only the release namespace is watched. |
| `watch.labelSelector` | `""` | Only reconcile KCLRun objects carrying these labels. |
| `watch.configsLabelSelector` | `"reconcile.fluxcd.io/watch=Enabled"` | Label selector for the ConfigMaps and Secrets a KCLRun references. |
| `defaultServiceAccount` | `""` | Service account to impersonate when a KCLRun does not name one. |
| `leaderElection.enabled` | `true` | Enable leader election. |
| `logLevel` | `info` | One of `trace`, `debug`, `info`, `error`. |
| `logEncoding` | `json` | `json` or `console`. |
| `minRetryDelay` | `750ms` | Minimum backoff between retries of a failed KCLRun. |
| `maxRetryDelay` | `15m` | Maximum backoff between retries of a failed KCLRun. |
| `requeueDependency` | `30s` | How often failing dependencies are reevaluated. |
| `httpRetry` | `9` | Retries when fetching artifacts over HTTP. |
| `kubeApi.qps` | `50` | Client-side queries-per-second limit for the Kubernetes API. |
| `kubeApi.burst` | `300` | Client-side burst limit for the Kubernetes API. |
| `insecureKubeconfig.exec` | `false` | Allow the `user.exec` section of a KCLRun supplied kubeconfig. |
| `insecureKubeconfig.tls` | `false` | Allow a KCLRun supplied kubeconfig to skip TLS verification. |
| `overrideManagers` | `[]` | Field managers disallowed from changing resources the controller manages. |
| `extraFlags` | `[]` | Extra `--flag=value` arguments, as a list of `{name, value}`. |
| `extraArgs` | `[]` | Extra arguments appended verbatim. |
| `tmpVolume.sizeLimit` | `""` | Size limit of the `/tmp` emptyDir. |
| `resources` | see `values.yaml` | Container resource requests and limits. |
| `podAnnotations` | Prometheus scrape annotations | Annotations for the controller pods. Ignored when metrics are disabled. |
| `podLabels` | `{}` | Extra labels for the controller pods. |
| `podSecurityContext` | `{}` | Pod-level security context. |
| `securityContext` | no privilege escalation, read-only root | Container security context. |
| `nodeSelector` | `{}` | Node selector for the controller pods. |
| `tolerations` | `[]` | Tolerations for the controller pods. |
| `affinity` | `{}` | Affinity rules for the controller pods. |
| `priorityClassName` | `""` | PriorityClass for the controller pods. |
| `terminationGracePeriodSeconds` | `10` | Grace period on pod termination. |
| `updateStrategy` | `{}` | Deployment update strategy. |

## CRDs

The KCLRun CRD lives in the chart's `crds/` directory, which Helm installs
before everything else and never deletes on uninstall. Two consequences:

- It cannot be toggled from `values.yaml`. Pass `--skip-crds` to omit it, or
  manage the CRD out of band.
- Helm never upgrades CRDs from `crds/`, so a chart upgrade will leave the CRD
  at the version it was installed with. When installing with Flux, set
  `spec.upgrade.crds: CreateReplace` on the HelmRelease to keep it in step, as
  `install/rc.yaml` does.

## cluster-admin

`rbac.clusterAdminBinding.create` defaults to `true`, matching the upstream
`config/default` manifests. The controller's job is to apply whatever manifests a
KCLRun renders, on behalf of whoever wrote that KCLRun, and those manifests can
target any resource in any namespace, so a narrow set of rules cannot be derived
up front.

If your KCLRun objects only ever touch a known set of resources, set this to
`false` and grant exactly what they need through `rbac.extraRules`. The chart
refuses to render if you disable the binding without adding any rules, rather
than leaving the controller silently unable to apply anything.

## Values validation

The chart fails at render time, rather than at apply time, when a value
combination would leave the controller unable to do its job:

- `rbac.create=true` with `rbac.clusterAdminBinding.create=false` and no
  `rbac.extraRules`
- `rbac.create=false` with no existing service account named