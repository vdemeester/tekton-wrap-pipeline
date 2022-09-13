# Tekton *Wrap* Pipeline Custom Task

Tekton resolver that allows to run a `Pipeline` with `emptydir`
workspaces that will be using different mean to transfer data from a
one `Task` to the other.

This is a experimentation around not using PVC for sharing data with
workspace in a Pipeline.

There is currently one type, `oci`, but the goal is to support
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
cluster, in the `tekton-pipelines-resolvers` namespaces.

## Wrapping with `oci`

```
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
  - name: cache
  tasks:
  - name: grab-source
    params:
    - name: url
      value: $(params.git-url)
    workspaces:
    - name: output
      workspace: sources
    - name: cache
      workspace: cache
    taskSpec:
      params:
      - name: url
        type: string
      workspaces:
      - name: output
      - name: cache
      steps:
      - name: clone
        image: gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/git-init:v0.21.0
        script: |
          /ko-app/git-init -url=$(params.url) -revision=main -path=$(workspaces.output.path)
          echo "foo" > $(workspaces.cache.path)/bar
  - name: build
    runAfter: [grab-source]
    workspaces:
    - name: sources
      workspace: sources
    - name: cache
      workspace: cache
    taskSpec:
      workspaces:
      - name: sources
      - name: cache
      steps:
      - name: build
        image: docker.io/library/golang:latest
        workingdir: $(workspaces.sources.path)
        script: |
          pwd && ls -la && go build -v ./...
          cat $(workspaces.cache.path)/bar
---
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: simple-pipelinerun
spec:
  serviceAccountName: mysa
  pipelineRef:
    resolver: wrap
    params:
    - name: pipelineref
      value: simple-pipeline
    - name: workspaces
      value: sources,cache
    - name: target
      value: quay.io/vdemeest/pipelinerun-$(context.pipelineRun.name)-{{workspace}}:latest
  params:
  - name: git-url
    value: https://github.com/vdemeester/go-helloworld-app
  workspaces:
  - name: sources
    emptyDir: {}
  - name: cache
    emptyDir: {}
```

The controller picks up the following parameters as it's own
configuration:
- `pipelineref`: which pipeline to fetch. *Note: later, we might
  support delegating to other resolvers*
- `workspaces`: comma separated list of workspace to "wrap"
- `target`: this is the oci image reference to push to. It's possible
  (and recommended) to use `{{workspace}}` to have different image for
  different workspaces. It's also possible to use
  `$(context.run.name)` to include the name of the run into the
  reference.
- `base`: this is the *initial* base image to use for
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
  that problem. We "could" try to use `results`, but it would consume
  result "resource" from the user.
