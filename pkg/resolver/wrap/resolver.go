package wrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tektoncd/pipeline/pkg/resolution/common"
	"github.com/tektoncd/pipeline/pkg/resolution/resolver/framework"
	"k8s.io/client-go/kubernetes"
	"knative.dev/pkg/client/injection/kube/client"
)

// LabelValueWrapResolverType is the value to use for the
// resolution.tekton.dev/type label on resource requests
const LabelValueWrapResolverType string = "wrap"

// TODO(sbwsg): This should be exposed as a configurable option for
// admins (e.g. via ConfigMap)
const timeoutDuration = time.Minute

// Resolver implements a framework.Resolver that can "wrap" a Pipeline for not using a PVC for workspaces
type Resolver struct {
	kubeClientSet kubernetes.Interface
}

// Initialize sets up any dependencies needed by the Resolver. None atm.
func (r *Resolver) Initialize(ctx context.Context) error {
	r.kubeClientSet = client.Get(ctx)
	return nil
}

// GetName returns a string name to refer to this Resolver by.
func (r *Resolver) GetName(context.Context) string {
	return "wrapresolver"
}

// GetConfigName returns the name of the wrap resolver's configmap.
func (r *Resolver) GetConfigName(context.Context) string {
	return "wrapresolver-config"
}

// GetSelector returns a map of labels to match requests to this Resolver.
func (r *Resolver) GetSelector(context.Context) map[string]string {
	return map[string]string{
		common.LabelKeyResolverType: LabelValueWrapResolverType,
	}
}

// ValidateParams ensures parameters from a request are as expected.
func (r *Resolver) ValidateParams(ctx context.Context, params map[string]string) error {
	fmt.Println("params", params)
	// FIXME: to implement
	return nil
}

// Resolve uses the given params to resolve the requested file or resource.
func (r *Resolver) Resolve(ctx context.Context, params map[string]string) (framework.ResolvedResource, error) {
	return nil, errors.New("Not implemented")
}
