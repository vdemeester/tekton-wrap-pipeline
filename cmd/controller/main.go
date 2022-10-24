package main

import (
	"github.com/openshift-pipelines/tekton-wrap-pipeline/internal/pkg/resolver/oci"
	"github.com/tektoncd/pipeline/pkg/apis/resolution/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/resolution/resolver/framework"
	filteredinformerfactory "knative.dev/pkg/client/injection/kube/informers/factory/filtered"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
)

const (
	// ControllerLogKey is the name of the logger for the controller cmd
	ControllerLogKey = "tekton-wrap-pipelines-controller"
)

func main() {
	ctx := filteredinformerfactory.WithSelectors(signals.NewContext(), v1alpha1.ManagedByLabelKey)

	sharedmain.MainWithContext(ctx, ControllerLogKey,
		framework.NewController(ctx, &oci.Resolver{}),
	)
}
