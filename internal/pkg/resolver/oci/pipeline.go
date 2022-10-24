package oci

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"knative.dev/pkg/logging"

	v1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"github.com/tektoncd/pipeline/pkg/apis/resolution/v1alpha1"
	"github.com/tektoncd/pipeline/pkg/resolution/common"
	"github.com/tektoncd/pipeline/pkg/resolution/resource"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/apis"
	"sigs.k8s.io/yaml"
)

func ResolvePipeline(ctx context.Context, r *Resolver, namespace, pipelineRef string) (*v1beta1.Pipeline, error) {
	logger := logging.FromContext(ctx)
	if strings.Contains(pipelineRef, "resolver:") {
		// This is an embedded resolver, pass through it.
		ref := &v1beta1.ResolverRef{}
		if err := yaml.Unmarshal([]byte(pipelineRef), ref); err != nil {
			return nil, err
		}
		logger.Infof("ResolverRef: %+v", ref)
		parameters := map[string]string{}
		for _, param := range ref.Params {
			// We only support strings params for now anyway
			parameters[param.Name] = param.Value.StringVal
		}
		name, err := resource.GenerateDeterministicName(string(ref.Resolver), "wrap", parameters)
		if err != nil {
			return nil, err
		}
		rr, err := r.rrclientSet.ResolutionV1alpha1().ResolutionRequests(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if !errors.IsNotFound(err) {
				return nil, err
			}
			request := &v1alpha1.ResolutionRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name: name,
					Labels: map[string]string{
						common.LabelKeyResolverType: string(ref.Resolver),
					},
				},
				Spec: v1alpha1.ResolutionRequestSpec{
					Parameters: parameters,
				},
			}

			logger.Infof("Create requestresolver %s", name)
			rr, err = r.rrclientSet.ResolutionV1alpha1().ResolutionRequests(namespace).Create(ctx, request, metav1.CreateOptions{})
			if err != nil {
				return nil, err
			}
		}

		if err := wait.PollImmediate(100*time.Millisecond, 10*time.Second, func() (bool, error) {
			select {
			case <-ctx.Done():
				return true, ctx.Err()
			default:
			}
			rr, err = r.rrclientSet.ResolutionV1alpha1().ResolutionRequests(namespace).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if rr.Status.GetCondition(apis.ConditionSucceeded).IsTrue() {
				return true, nil
			} else if rr.Status.GetCondition(apis.ConditionSucceeded).IsFalse() {
				return true, fmt.Errorf("Error: %s", rr.Status.GetCondition(apis.ConditionSucceeded).Reason)
			}
			return false, nil
		}); err != nil {
			return nil, err
		}

		pipeline := &v1beta1.Pipeline{}
		decodedBytes, err := base64.StdEncoding.Strict().DecodeString(rr.Status.Data)
		if err != nil {
			return nil, fmt.Errorf("error decoding data from base64: %w", err)
		}
		logger.Infof("data: %s", string(decodedBytes))
		if err := yaml.Unmarshal(decodedBytes, pipeline); err != nil {
			return nil, err
		}

		// Delete the created resolutionrequest
		// FIXME: remove this once we can get enough information to set ownerRef properly
		err = r.rrclientSet.ResolutionV1alpha1().ResolutionRequests(namespace).Delete(ctx, name, metav1.DeleteOptions{})

		return pipeline, nil

	}
	pipeline, err := r.pipelineClientSet.TektonV1beta1().Pipelines(namespace).Get(ctx, pipelineRef, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return pipeline, nil
}
