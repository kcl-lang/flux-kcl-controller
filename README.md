<h1 align="center">Flux KCL controller</h1>

<p align="center">
<a href="./README.md">English</a> | <a href="./README-zh.md">简体中文</a>
</p>

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fkcl-lang%2Fflux-kcl-controller.svg?type=shield)](https://app.fossa.com/projects/git%2Bgithub.com%2Fkcl-lang%2Fflux-kcl-controller?ref=badge_shield)

# Introduction

The `flux-kcl-controller` is a component developed for the integration of [KCL](https://github.com/kcl-lang/kcl) and [Flux](https://github.com/fluxcd/flux2), designed to orchestrate continuous delivery pipelines for infrastructure and workloads defined with KCL based on the [source-controller](https://github.com/fluxcd/source-controller), [kustomize-controller](https://github.com/fluxcd/kustomize-controller) and [helm-controller](https://github.com/fluxcd/helm-controller) to acquire the KCL program from repositories.

![kcl-flux](./docs/img/kcl-flux.png)

# Features

- Periodically monitor git repositories that store KCL programs and reconcile k8s cluster status according to changes in git repositories.

# Installation

The controller requires [Flux](https://github.com/fluxcd/flux2) to be installed
first, because it reconciles KCLRun objects against the Flux source CRDs
(`GitRepository`, `OCIRepository`, `Bucket`) and `Kustomization`.

## With Flux (GitOps)

`install/rc.yaml` is a `GitRepository` and a `HelmRelease` pointing at the chart
in this repository, so nothing has to be published to a chart registry first:

```shell
kubectl apply -f https://raw.githubusercontent.com/kcl-lang/flux-kcl-controller/main/install/rc.yaml
```

It tracks `main` by default. Pin `spec.chart.spec.ref` to a tag if you would
rather follow a release.

## With Helm

```shell
helm install flux-kcl-controller ./charts/flux-kcl-controller \
  --namespace flux-system --create-namespace
```

## With kustomize

```shell
kustomize build config/default | kubectl apply -f -
```

See [charts/flux-kcl-controller/README.md](./charts/flux-kcl-controller/README.md)
for the values the chart accepts.

# Quick Start

## Prerequisites

### For Linux/Mac Users:
- k3d: used to create a k8s cluster for testing, if you already have a k8s cluster, you can skip ignore this.
- Kustomize
- Kubectl

### For Windows Users:
**Install WSL (Windows Subsystem for Linux):**
1. Open PowerShell as Administrator and run:
   ```powershell
   wsl --install 
2. Restart your computer.
3. Open WSL terminal and install required tools:
``` 
sudo apt update
sudo apt install make kubectl git

# Install k3d
curl -s https://raw.githubusercontent.com/k3d-io/k3d/main/install.sh | bash

# Install kustomize
curl -s "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash
sudo mv kustomize /usr/local/bin/
```
### Note: All subsequent commands are the same for all platforms

## Create a test k8s cluster

Create a cluster using the following command:

```shell
k3d cluster create
```

## Download kcl-controller and install it into the cluster

Clone this repository to local:

```shell
git clone https://github.com/kcl-lang/flux-kcl-controller.git
```

Enter the root directory of this repository:

```shell
cd flux-kcl-controller
```

Install kcl-controller into the cluster:

```shell
make deploy
```

## Monitor a git repository

Take the GitHub repository https://github.com/awesome-kusion/kcl-deployment as an example. This repository stores a KCL program that defines a `Deployment`. We will use `flux-kcl-controller` to deploy this program.

Define a `GitRepository` object through the `gitrepo.yaml` file to monitor the repository:

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: kcl-deployment
  namespace: source-system
spec:
  interval: 30s
  url: https://github.com/awesome-kusion/kcl-deployment.git
  ref:
    branch: main
---
apiVersion: krm.kcl.dev.fluxcd/v1alpha1
kind: KCLRun
metadata:
  name: kcl-deployment
  namespace: source-system
spec:
  sourceRef:
    kind: GitRepository
    name: kcl-deployment
```

Use the command `kubectl apply -f gitrepo.yaml` to deploy the object to the cluster.

## View the deployment result

Use the command `kubectl get deployment` to view the deployment result:

```shell
NAME               READY   UP-TO-DATE   AVAILABLE   AGE
nginx-deployment   1/1     1            0           28s
```

The `nginx-deployment` is deployed successfully.

## Update the KCL program in the repository

We can update the `Deployment` in the cluster by modifying the KCL program in the repository.

Change the version of nginx from `1.7.7` to `1.7.8` and the name of `deployment` to `nginx-deployment-1`, and commit to the main branch.

The changes can be referred to: [nginx:1.7.7 deployment](https://github.com/awesome-kusion/kcl-deployment/commit/dc8b2aa70b1b47bef0637212ea184792b8c43449) -> [nginx:1.7.8 deployment](https://github.com/awesome-kusion/kcl-deployment/commit/f257a71fdff6cb9190f49c1dbf5fa4496d7b3cb2)

Use the command `kubectl get deployment` to view the deployment result:


```shell
NAME                 READY   UP-TO-DATE   AVAILABLE   AGE
nginx-deployment     1/1     1            1           20m
nginx-deployment-1   1/1     1            0           4s
```

`flux-kcl-controller` creates a `nginx-deployment-1` according to the KCL program in the repository.
