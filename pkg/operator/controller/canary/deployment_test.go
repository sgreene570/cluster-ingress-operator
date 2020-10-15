package canary

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestDesiredCanaryDeployment(t *testing.T) {
	canaryImage := "openshift/hello-openshift:latest"

	deployment := desiredCanaryDeployment(canaryImage)

	expectedDeploymentName := types.NamespacedName{
		Namespace: "openshift-ingress-canary",
		Name:      "ingress-canary",
	}

	if !cmp.Equal(deployment.Name, expectedDeploymentName.Name) {
		t.Errorf("Expected deployment name to be %s, but got %s", expectedDeploymentName.Name, deployment.Name)
	}

	if !cmp.Equal(deployment.Namespace, expectedDeploymentName.Namespace) {
		t.Errorf("Expected deployment namespace to be %s, but got %s", expectedDeploymentName.Namespace, deployment.Namespace)
	}

	expectedLabels := map[string]string{
		"ingress.openshift.io/canary": "canary_controller",
	}

	if !cmp.Equal(deployment.Labels, expectedLabels) {
		t.Errorf("Expected deployment labels to be %q, but got %q", expectedLabels, deployment.Labels)
	}

	labelSelector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "ingress-canary",
		},
	}

	if !cmp.Equal(deployment.Spec.Selector, labelSelector) {
		t.Errorf("Expected deployment selector to be %q, but got %q", labelSelector, deployment.Spec.Selector)
	}

	if !cmp.Equal(deployment.Spec.Template.Labels, labelSelector.MatchLabels) {
		t.Errorf("Expected deployment template labels to be %q, but got %q", labelSelector.MatchLabels, deployment.Spec.Template.Labels)
	}

	containers := deployment.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Errorf("Expected deployment to have 1 container, but found %d", len(containers))
	}

	if !cmp.Equal(containers[0].Image, canaryImage) {
		t.Errorf("Expected deployment container image to be %q, but got %q", canaryImage, containers[0].Image)
	}
}
