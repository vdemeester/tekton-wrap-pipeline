# Tekton *Wrap* Pipeline Custom Task

Tekton [custom
task](https://github.com/tektoncd/pipeline/blob/main/docs/runs.md)
that allows to run a `Pipeline` with `emptydir` workspaces that will
be using different mean to transfer data from a one `Task` to the
other.

This is a experimentation around not using PVC for sharing data with
workspace in a Pipeline.

There is currently one type, `OCIPipeline`, but the goal is to support
more like `rsync`, `s3`, `gcs`, …

## Install

To install this custom task, you will need
[`ko`](https://github.com/google/ko) until a release is being
published.

```bash
# In a checkout of this repository
$ export KO_DOCKER_REPO={prefix-for-image-reference} # e.g.: quay.io/vdemeest
$ ko apply -f config
2022/03/28 14:53:16 Using base gcr.io/distroless/static:nonroot@sha256:2556293984c5738fc75208cce52cf0a4762c709cf38e4bf8def65a61992da0ad for github.com/openshift-pipelines/tekton-wrap-pipeline/cmd/controller
# […]
deployment.apps/tekton-wrap-pipeline-controller configured
```

This will build and install the custom task controller on your
cluster, in the `tekton-pipelines` namespaces.

## `OCIPipeline`

Tekton [custom
task](https://github.com/tektoncd/pipeline/blob/main/docs/runs.md)
that allows to run a `Pipeline` with `emptydir` workspaces that will
be using oci images to transfer data from a one `Task` to the other.

This is a experimentation around not using PVC for sharing data with
workspace in a Pipeline.

### Usage

#### Using `Run`

To run a `simple-pipeline` `Pipeline` defined below, we can just define a `Run`
and refer to the `Pipeline` with `OCIPipeline` as a type. The type
`OCIPipeline` doesn't really exists but the `Run` gets picked up by
the controller as its own.

```yaml
apiVersion: tekton.dev/v1beta1
kind: Pipeline
metadata:
  name: simple-pipeline
spec:
  params:
  - name: git-url
    type: string
    default: https://github.com/vdemeester/buildkit-tekton
  workspaces:
  - name: sources
  tasks:
  - name: grab-source
    params:
    - name: url
      value: $(params.git-url)
    workspaces:
    - name: output
      workspace: sources
    taskSpec:
      params:
      - name: url
        type: string
      workspaces:
      - name: output
      steps:
      - name: clone
        image: gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/git-init:v0.21.0
        script: |
          /ko-app/git-init -url=$(params.url) -revision=main -path=$(workspaces.output.path)
  - name: build
    runAfter: [grab-source]
    workspaces:
    - name: sources
      workspace: sources
    taskSpec:
      workspaces:
      - name: sources
      steps:
      - name: build
        image: docker.io/library/golang:latest
        workingdir: $(workspaces.sources.path)
        script: |
          pwd && ls -la && go build -v ./...
---
apiVersion: tekton.dev/v1alpha1
kind: Run
metadata:
  name: run-simple-pipeline
spec:
  serviceAccountName: ocipipeline-sa
  ref:
    apiVersion: tekton.dev/v1alpha1
    kind: OCIPipeline
    name: simple-pipeline
  params:
  #- name: ocipipeline.base
  #  value: docker.io/vdemeester/oci-workspace-base:latest
  - name: ocipipeline.target
    value: docker.io/vdemeester/pipelinerun-$(context.run.name)-{{workspace}}:latest
  - name: git-url
    value: https://github.com/vdemeester/go-helloworld-app
  workspaces:
  - name: sources
    emptyDir: {}
```

The controller picks up all workspace with `emptydir` specified in the
`Run` and back them up with an oci image instead.

The controller picks up the following parameters as it's own
configuration:
- `ocipipeline.target`: this is the oci image reference to push
  to. It's possible (and recommended) to use `{{workspace}}` to have
  different image for different workspaces. It's also possible to use
  `$(context.run.name)` to include the name of the run into the
  reference.
- `ocipipeline.base`: this is the *initial* base image to use for
  workspaces. The default is
  `ghcr.io/openshift-pipelines/tekton-wrap-pipeline/base:latest` which comes from
  [`./images/base`](./images/base).

## Limitations

- How to handle parallel task ?
- What differs from today ?
  If you use a workspace in several task that are not dependent on
  each other, what happens ? And what "should" happen for the oci
  wrapper ?

The way it might/should work :
- Each step adds a layer (with a diff) *and* each time it is using a
  different tag (?) or maybe it could just use 1 tag and refer to the
  previously pushed digest.
  The trick here is, we cannot know the digest before, so we have to
  rely on tag. If we use only one tag, we *may* run into trouble when
  tasks are running into parallel with the same workspace, as one will
  override the previous one. We could also "rebase" on top of the
  latest build just before pushing, which would "remove" a tiny bit
  that problem.
