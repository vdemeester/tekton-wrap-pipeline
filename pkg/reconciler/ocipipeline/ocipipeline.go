package ocipipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	clientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/typed/pipeline/v1beta1"
	runreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1alpha1/run"
	listersalpha "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1alpha1"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/reconciler/events"
	"go.uber.org/zap"
	"gomodules.xyz/jsonpatch/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"knative.dev/pkg/apis"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
)

const (
	DefaultBaseImage = "ghcr.io/vdemeester/tekton-oci-pipeline/base:latest"

	BaseParamName   = "ocipipeline.base"
	TargetParamName = "ocipipeline.target"

	// ReasonRunFailedValidation indicates that the reason for failure status is that Run failed validation
	ReasonRunFailedValidation = "ReasonRunFailedValidation"

	// ReasonRunFailedCreatingPipelineRun indicates that the reason for failure status is that Run failed
	// to create PipelineRun
	ReasonRunFailedCreatingPipelineRun = "ReasonRunFailedCreatingPipelineRun"
)

// Reconciler implements controller.Reconciler for Configuration resources.
type Reconciler struct {
	pipelineClientSet clientset.Interface
	kubeClientSet     kubernetes.Interface
	runLister         listersalpha.RunLister
	pipelineRunLister listers.PipelineRunLister
}

var (
	// Check that our Reconciler implements runreconciler.Interface
	_                runreconciler.Interface = (*Reconciler)(nil)
	cancelPatchBytes []byte
)

func init() {
	var err error
	patches := []jsonpatch.JsonPatchOperation{{
		Operation: "add",
		Path:      "/spec/status",
		Value:     v1beta1.TaskRunSpecStatusCancelled,
	}}
	cancelPatchBytes, err = json.Marshal(patches)
	if err != nil {
		log.Fatalf("failed to marshal patch bytes in order to cancel: %v", err)
	}
}

// ReconcileKind compares the actual state with the desired, and attempts to converge the two.
// It then updates the Status block of the Run resource with the current status of the resource.
func (c *Reconciler) ReconcileKind(ctx context.Context, run *v1alpha1.Run) pkgreconciler.Event {
	var merr error
	logger := logging.FromContext(ctx)
	logger.Infof("Reconciling Run %s/%s at %v", run.Namespace, run.Name, time.Now())

	// Check that the Run references a TaskGroup CRD.  The logic is controller.go should ensure that only this type of Run
	// is reconciled this controller but it never hurts to do some bullet-proofing.
	if run.Spec.Ref == nil ||
		run.Spec.Ref.APIVersion != v1alpha1.SchemeGroupVersion.String() || run.Spec.Ref.Kind != kind {
		logger.Warn("Should not have been notified about Run %s/%s; will do nothing", run.Namespace, run.Name)
		return nil
	}

	// If the Run has not started, initialize the Condition and set the start time.
	if !run.HasStarted() {
		logger.Infof("Starting new Run %s/%s", run.Namespace, run.Name)
		run.Status.InitializeConditions()
		// In case node time was not synchronized, when controller has been scheduled to other nodes.
		if run.Status.StartTime.Sub(run.CreationTimestamp.Time) < 0 {
			logger.Warnf("Run %s createTimestamp %s is after the Run started %s", run.Name, run.CreationTimestamp, run.Status.StartTime)
			run.Status.StartTime = &run.CreationTimestamp
		}
		// Emit events. During the first reconcile the status of the Run may change twice
		// from not Started to Started and then to Running, so we need to sent the event here
		// and at the end of 'Reconcile' again.
		// We also want to send the "Started" event as soon as possible for anyone who may be waiting
		// on the event to perform user facing initialisations, such has reset a CI check status
		afterCondition := run.Status.GetCondition(apis.ConditionSucceeded)
		events.Emit(ctx, nil, afterCondition, run)
	}

	if run.IsDone() {
		logger.Infof("Run %s/%s is done", run.Namespace, run.Name)
		return nil
	}

	// Store the condition before reconcile
	beforeCondition := run.Status.GetCondition(apis.ConditionSucceeded)

	// Reconcile the Run
	if err := c.reconcile(ctx, run); err != nil {
		logger.Errorf("Reconcile error: %v", err.Error())
		merr = multierror.Append(merr, err)
	}

	if err := c.updateLabelsAndAnnotations(ctx, run); err != nil {
		logger.Warn("Failed to update Run labels/annotations", zap.Error(err))
		merr = multierror.Append(merr, err)
	}

	afterCondition := run.Status.GetCondition(apis.ConditionSucceeded)
	events.Emit(ctx, beforeCondition, afterCondition, run)

	// Only transient errors that should retry the reconcile are returned.
	return merr
}

func (c *Reconciler) reconcile(ctx context.Context, run *v1alpha1.Run) error {
	logger := logging.FromContext(ctx)

	pr, err := getPipelineRunIfExists(c.pipelineRunLister, run.Namespace, run.Name)
	if err != nil {
		logger.Errorf("Run %s/%s got an error fetching taskRun: %v", run.Namespace, run.Name, err)
		return fmt.Errorf("couldn't fetch pipelinerun %v", err)
	}
	if pr != nil {
		logger.Infof("Found a PipelineRun object %s", pr.Name)
		return updateRunStatus(ctx, run, pr)
	}

	// FIXME validate things
	baseimage := DefaultBaseImage
	targetimage := ""
	pipelinerunParams := []v1beta1.Param{}
	for _, p := range run.Spec.Params {
		switch p.Name {
		case BaseParamName:
			baseimage = p.Value.StringVal
			continue
		case TargetParamName:
			targetimage = p.Value.StringVal
			continue
		default:
			pipelinerunParams = append(pipelinerunParams, p)
		}
	}
	if targetimage == "" {
		run.Status.MarkRunFailed(ReasonRunFailedValidation,
			"No targetImage specified")
	} else {
		targetimage = strings.ReplaceAll(targetimage, "$(context.run.name)", run.Name)
	}

	// Let's..
	// - Grab all "workspaces" to "hijack"
	ociworkspaces := map[string]v1beta1.WorkspaceBinding{}
	for _, w := range run.Spec.Workspaces {
		if w.EmptyDir != nil {
			ociworkspaces[w.Name] = w
		}
	}
	// - resolve the pipeline
	spec, err := getPipelineSpec(ctx, c.pipelineClientSet.TektonV1beta1(), run.Namespace, run.Spec.Ref.Name)
	if err != nil {
		run.Status.MarkRunFailed(ReasonRunFailedValidation,
			"Pipeline couldn't be fetched - %v", err)
		return controller.NewPermanentError(fmt.Errorf("run %s/%s is invalid because of %v", run.Namespace, run.Name, err))
		// return fmt.Errorf("couldn't fetch pipeline spec %s: %v", run.Spec.Ref.Name, err)
	}
	taskSpecs, err := getTaskSpecs(ctx, c.pipelineClientSet.TektonV1beta1(), spec, run.Namespace)
	if err != nil {
		run.Status.MarkRunFailed(ReasonRunFailedValidation,
			"Not all of the pipeline's tasks could be fetched - %v", err)
		return fmt.Errorf("couldn't fetch pipeline's tasks: %v", err)
	}

	// Create name for TaskRun from Run name plus iteration number.
	pipelinerun := &v1beta1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:            run.Name,
			Namespace:       run.Namespace,
			OwnerReferences: []metav1.OwnerReference{*kmeta.NewControllerRef(run)},
			// TODO handle labels, annotations
		},
		Spec: v1beta1.PipelineRunSpec{
			ServiceAccountName: run.Spec.ServiceAccountName,
			Params:             pipelinerunParams,
			PodTemplate:        run.Spec.PodTemplate,
			Workspaces:         run.Spec.Workspaces,
			PipelineSpec: &v1beta1.PipelineSpec{
				Tasks:  []v1beta1.PipelineTask{},
				Params: spec.Params,
				// FIXME We bypass this for now, long term, we "filter"
				Workspaces: spec.Workspaces,
				Results:    spec.Results,
				// FIXME Add support for finally
			},
		},
	}

	// - enhance the pipeline with steps
	sequence, err := putTasksInOrder(spec.Tasks)
	if err != nil {
		return fmt.Errorf("couldn't find valid order for tasks: %v", err)
	}
	for i, taskspec := range sequence {
		s := taskSpecs[taskspec.Name]
		// FIXME support filtering workspaces
		for _, pw := range taskspec.Workspaces {
			if _, ok := ociworkspaces[pw.Workspace]; !ok {
				// Skip "unknown" workspaces
				continue
			}
			wtargetimage := strings.ReplaceAll(targetimage, "{{workspace}}", pw.Workspace)
			var w v1beta1.WorkspaceDeclaration
			for _, d := range s.Workspaces {
				if d.Name == pw.Name {
					w = d
				}
			}
			// "enhance" the spec
			if i != 0 {
				baseimage = wtargetimage
				// Add an extra step
				s.Steps = append([]v1beta1.Step{{
					Container: corev1.Container{
						Name:       "import-workspace",
						Image:      "gcr.io/go-containerregistry/crane:debug",
						WorkingDir: "/",
					},
					Script: fmt.Sprintf(`#!/busybox/sh
echo "Extract workspace content from %s in %s"
crane export %s | tar -x -C %s`, baseimage, w.GetMountPath(), baseimage, w.GetMountPath()),
				}}, s.Steps...)
			}
			s.Steps = append(s.Steps, v1beta1.Step{
				Container: corev1.Container{
					Name:  "export-workspace",
					Image: "gcr.io/go-containerregistry/crane:debug",
				},
				Script: fmt.Sprintf(`#!/busybox/sh
echo "Export workspace content from %s to %s"
cd %s && tar -f - -c . | crane append -b %s -t %s -f -`, w.GetMountPath(), wtargetimage, w.GetMountPath(), baseimage, wtargetimage),
			})
		}
		taskspec.TaskSpec.TaskSpec = *s
		pipelinerun.Spec.PipelineSpec.Tasks = append(pipelinerun.Spec.PipelineSpec.Tasks, taskspec)
	}

	logger.Infof("PipelineRun to create: %+v", pipelinerun)
	// - create a PipelineRun to run it
	if _, err := c.pipelineClientSet.TektonV1beta1().PipelineRuns(run.Namespace).Create(ctx, pipelinerun, metav1.CreateOptions{}); err != nil {
		logger.Errorf("Run %s/%s got an error creating PipelineRun - %v", run.Namespace, run.Name, err)
		run.Status.MarkRunFailed(ReasonRunFailedCreatingPipelineRun,
			"Run got an error creating pipelineRun - %v", err)
		return err
	}

	return nil
}

func (c *Reconciler) updateLabelsAndAnnotations(ctx context.Context, run *v1alpha1.Run) error {
	newRun, err := c.runLister.Runs(run.Namespace).Get(run.Name)
	if err != nil {
		return fmt.Errorf("error getting Run %s when updating labels/annotations: %w", run.Name, err)
	}
	if !reflect.DeepEqual(run.ObjectMeta.Labels, newRun.ObjectMeta.Labels) || !reflect.DeepEqual(run.ObjectMeta.Annotations, newRun.ObjectMeta.Annotations) {
		mergePatch := map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels":      run.ObjectMeta.Labels,
				"annotations": run.ObjectMeta.Annotations,
			},
		}
		patch, err := json.Marshal(mergePatch)
		if err != nil {
			return err
		}
		_, err = c.pipelineClientSet.TektonV1alpha1().Runs(run.Namespace).Patch(ctx, run.Name, types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	}
	return nil
}

func updateRunStatus(ctx context.Context, run *v1alpha1.Run, pipelineRun *v1beta1.PipelineRun) error {
	logger := logging.FromContext(ctx)

	c := pipelineRun.GetStatusCondition().GetCondition(apis.ConditionSucceeded)
	if c.IsTrue() {
		logger.Infof("TaskRun created by Run %s/%s has succeeded", run.Namespace, run.Name)
		run.Status.MarkRunSucceeded(c.Reason, c.Message)
	} else if c.IsFalse() {
		logger.Infof("TaskRun created by Run %s/%s has failed", run.Namespace, run.Name)
		run.Status.MarkRunFailed(c.Reason, c.Message)
	} else if c.IsUnknown() {
		logger.Infof("TaskRun created by Run %s/%s is still running", run.Namespace, run.Name)

		reason, message := "", ""
		if c != nil {
			reason, message = c.Reason, c.Message
		}
		run.Status.MarkRunRunning(reason, message)
	} else {
		logger.Errorf("TaskRun created by Run %s/%s has an unexpected ConditionSucceeded", run.Namespace, run.Name)
		return fmt.Errorf("unexpected ConditionSucceded - %s", c)
	}

	return nil
}

func getPipelineRunIfExists(lister listers.PipelineRunLister, namespace, name string) (*v1beta1.PipelineRun, error) {
	pr, err := lister.PipelineRuns(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("couldn't fetch taskrun %v", err)
	}
	return pr, nil
}

func getPipelineSpec(ctx context.Context, tv1beta1 tektonv1beta1.TektonV1beta1Interface, namespace, pipeline string) (*v1beta1.PipelineSpec, error) {
	p, err := tv1beta1.Pipelines(namespace).Get(ctx, pipeline, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &p.Spec, nil
}

func getTaskSpecs(ctx context.Context, tv1beta1 tektonv1beta1.TektonV1beta1Interface, pSpec *v1beta1.PipelineSpec, namespace string) (map[string]*v1beta1.TaskSpec, error) {
	taskSpecs := map[string]*v1beta1.TaskSpec{}
	for _, ptask := range pSpec.Tasks {
		var taskSpec *v1beta1.TaskSpec
		if ptask.TaskRef == nil {
			taskSpec = &ptask.TaskSpec.TaskSpec
		} else {
			var err error
			taskSpec, err = getTaskSpec(ctx, tv1beta1, namespace, ptask.TaskRef.Name)
			if err != nil {
				return nil, fmt.Errorf("couldn't fetch taskspec for %s: %v", ptask.Name, err)
			}
		}
		taskSpecs[ptask.Name] = taskSpec
	}
	return taskSpecs, nil
}

func getTaskSpec(ctx context.Context, tv1beta1 tektonv1beta1.TektonV1beta1Interface, namespace, task string) (*v1beta1.TaskSpec, error) {
	t, err := tv1beta1.Tasks(namespace).Get(ctx, task, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &t.Spec, nil
}
