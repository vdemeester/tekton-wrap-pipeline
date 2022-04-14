package wrappipeline

import (
	"context"

	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	pipelineclient "github.com/tektoncd/pipeline/pkg/client/injection/client"
	runinformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1alpha1/run"
	pipelineruninformer "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1beta1/pipelinerun"
	runreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/run"
	pipelinecontroller "github.com/tektoncd/pipeline/pkg/controller"
	"k8s.io/client-go/tools/cache"
	kubeclient "knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/logging"
)

const (
	ControllerName = "ocipipeline-controller"
	kind           = "OCIPipeline"
)

// NewController instantiates a new controller.Impl from knative.dev/pkg/controller
func NewController() func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, cmw configmap.Watcher) *controller.Impl {

		logger := logging.FromContext(ctx)
		kubeclientset := kubeclient.Get(ctx)
		pipelineclientset := pipelineclient.Get(ctx)
		runInformer := runinformer.Get(ctx)
		pipelineRunInformer := pipelineruninformer.Get(ctx)

		c := &Reconciler{
			kubeClientSet:     kubeclientset,
			pipelineClientSet: pipelineclientset,
			runLister:         runInformer.Lister(),
			pipelineRunLister: pipelineRunInformer.Lister(),
		}

		impl := runreconciler.NewImpl(ctx, c, func(impl *controller.Impl) controller.Options {
			return controller.Options{
				AgentName: "run-ocipipeline",
			}
		})

		logger.Info("Setting up event handlers")

		// Add event handler for Runs
		runInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: pipelinecontroller.FilterRunRef(v1alpha1.SchemeGroupVersion.String(), kind),
			Handler:    controller.HandleAll(impl.Enqueue),
		})

		// Add event handler for TaskRuns controlled by Run
		pipelineRunInformer.Informer().AddEventHandler(cache.FilteringResourceEventHandler{
			FilterFunc: pipelinecontroller.FilterOwnerRunRef(runInformer.Lister(), v1alpha1.SchemeGroupVersion.String(), kind),
			Handler:    controller.HandleAll(impl.EnqueueControllerOf),
		})
		return impl
	}
}
