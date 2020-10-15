package canary

import (
	"context"
	"fmt"

	"github.com/openshift/cluster-ingress-operator/pkg/manifests"
	"github.com/openshift/cluster-ingress-operator/pkg/operator/controller"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
)

// ensureCanaryDeployment ensures the ingress canary deployment exists
func (r *reconciler) ensureCanaryDeployment() (bool, *appsv1.Deployment, error) {
	desired := desiredCanaryDeployment(r.Config.CanaryImage)
	haveDepl, current, err := r.currentCanaryDeployment()
	if err != nil {
		return false, nil, err
	}

	if haveDepl {
		return true, current, nil
	} else {
		err := r.createCanaryDeployment(desired)
		if err != nil {
			return false, nil, err
		}
	}
	return true, desired, nil
}

// currentCanaryDeployment returns the current ingress canary deployment
func (r *reconciler) currentCanaryDeployment() (bool, *appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	if err := r.client.Get(context.TODO(), controller.CanaryDeploymentName(), deployment); err != nil {
		if errors.IsNotFound(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, deployment, nil
}

// createCanaryDeployment creates the given deployment resource
func (r *reconciler) createCanaryDeployment(deployment *appsv1.Deployment) error {
	if err := r.client.Create(context.TODO(), deployment); err != nil {
		return fmt.Errorf("failed to create canary deployment %s/%s: %v", deployment.Namespace, deployment.Name, err)
	}

	log.Info("created canary deployment", "namespace", deployment.Namespace, "name", deployment.Name)
	return nil
}

// desiredCanaryDeployment returns the desired canary deployment read in
// from manifests
func desiredCanaryDeployment(canaryImage string) *appsv1.Deployment {
	deployment := manifests.CanaryDeployment()
	name := controller.CanaryDeploymentName()
	deployment.Name = name.Name
	deployment.Namespace = name.Namespace

	deployment.Labels = map[string]string{
		// associate the deployment with the ingress canary controller
		manifests.OwningIngressCanaryCheckLabel: controllerName,
	}

	deployment.Spec.Selector = controller.CanaryDeploymentPodSelector()
	deployment.Spec.Template.Labels = controller.CanaryDeploymentPodSelector().MatchLabels

	deployment.Spec.Template.Spec.Containers[0].Image = canaryImage

	return deployment
}
